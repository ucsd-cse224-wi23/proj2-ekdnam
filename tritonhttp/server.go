package tritonhttp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	responseProto = "HTTP/1.1"

	statusOK               = 200
	statusMethodNotAllowed = 405
	statusNotFound         = 404
	statusBadRequest       = 400

	HOST       = "Host"
	CONNECTION = "Connection"
	DATE       = "Date"
	PROTO      = "HTTP/1.1"
	MAXSIZE    = 10000
	// LAYOUT = "01 02 2006 15:04:05"
)

var statusText = map[int]string{
	statusOK:               "OK",
	statusMethodNotAllowed: "Method Not Allowed",
	statusNotFound:         "Not Found",
	statusBadRequest:       "Bad Request",
}

type Server struct {
	// Addr ("host:port") : specifies the TCP address of the server
	Addr string
	// DocRoot the root folder under which clients can potentially look up information.
	// Anything outside this should be "out-of-bounds"
	DocRoot string
	// VirtualHosts
	VirtualHosts map[string]string
}

func checkInDocroot(path string, docroot string) bool {
	dir := path
	for dir != "." {
		dir = filepath.Dir(dir)
		if dir == docroot {
			return true
		}
	}
	return false
}

func (s *Server) init() {
	s.DocRoot = "docroot_dirs"
	fmt.Println(s.VirtualHosts)
}
func (s *Server) ListenAndServe() error {
	// Validate the configuration of the server
	s.init()
	// if err := s.ValidateServerSetup(); err != nil {
	// 	return fmt.Errorf("server is not setup correctly %v", err)
	// }
	fmt.Println("Server setup valid!")

	// server should now start to listen on the configured address
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	fmt.Println("Listening on", ln.Addr())

	// making sure the listener is closed when we exit
	defer func() {
		err = ln.Close()
		if err != nil {
			fmt.Println("error in closing listener", err)
		}
	}()

	// accept connections forever
	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}
		fmt.Println("accepted connection", conn.RemoteAddr())
		go s.HandleConnection(conn)
	}
}

func prettyPrintReq(request *Request) {
	empJSON, err := json.MarshalIndent(*request, "", "  ")
	if err != nil {
		log.Fatalf(err.Error())
	}
	fmt.Printf("Request %s\n", string(empJSON))
}

func prettyPrintRes(response *Response) {
	tmp := ""
	if len(response.Body) > 0 {
		tmp = response.Body
		response.Body = ""
	}
	empJSON, err := json.MarshalIndent(*response, "", "  ")
	if err != nil {
		log.Fatalf(err.Error())
	}
	if tmp != "" {
		response.Body = tmp
	}
	fmt.Printf("Response %s\n", string(empJSON))

}

// HandleConnection reads requests from the accepted conn and handles them.
func (s *Server) HandleConnection(conn net.Conn) {
	br := bufio.NewReaderSize(conn, MAXSIZE)
	for {
		// Set timeout
		if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			log.Printf("Failed to set timeout for connection %v", conn)
			_ = conn.Close()
			return
		}

		// Read next request from the client
		req, bytes, err := ReadRequest(br)
		// Handle EOF
		if errors.Is(err, io.EOF) {
			log.Printf("Connection closed by %v", conn.RemoteAddr())
			// _ = conn.Close()
			// return
			continue
		}

		// timeout in this application means we just close the connection
		// Note : proj3 might require you to do a bit more here
		if err, ok := err.(net.Error); ok && err.Timeout() {
			if !bytes {
				log.Printf("Connection to %v timed out", conn.RemoteAddr())
				_ = conn.Close()
			} else {
				res := s.HandleBadRequest()
				err := res.Write(conn)
				if err != nil {
					fmt.Printf(err.Error())
				}
				_ = conn.Close()
			}
			return
		}

		if err != nil {
			log.Printf(err.Error())
			log.Printf("Handle bad request for error")
			res := &Response{}
			res.HandleBadRequest()
			_ = res.Write(conn)
			_ = conn.Close()
			return
		}

		prettyPrintReq(req)

		res := s.HandleGoodRequest()
		err = s.parseAndGenerateResponse(req, res)
		if req.Close {
			res.Headers["Connection"] = "close"
		}
		prettyPrintRes(res)
		// 404 error
		if err != nil {
			res := s.HandleNotFoundRequest()
			if req.Close {
				res.Headers["Connection"] = "close"
			}
			_ = res.Write(conn)
			if req.Close {
				err = conn.Close()
				if err != nil {
					fmt.Println("Error while closing connection")
				}
				return
			}
		}
		err = res.Write(conn)
		if err != nil {
			fmt.Println(err)
		}
		if req.Close {
			conn.Close()
			return
		}

		// We'll never close the connection and handle as many requests for this connection and pass on this
		// responsibility to the timeout mechanism
	}
}

// HTTP/1.1 200 OK | Connection close
func (s *Server) HandleCloseRequest() (res *Response) {
	res = &Response{}
	res.init()
	res.StatusCode = statusOK
	res.StatusText = statusText[statusOK]
	res.Headers[CONNECTION] = "close"
	return res
}

func (s *Server) HandleBadRequest() (res *Response) {
	res = &Response{}
	res.init()
	res.StatusCode = statusBadRequest
	res.StatusText = statusText[statusBadRequest]
	res.FilePath = ""
	res.Headers[CONNECTION] = "close"
	return res
}

// HTTP/1.1 200 OK
func (s *Server) HandleGoodRequest() (res *Response) {
	res = &Response{}
	res.HandleOK()
	return res
}

// HTTP/1.1 404 Not Found
func (s *Server) HandleNotFoundRequest() (res *Response) {
	res = &Response{}
	res.init()
	res.StatusCode = statusNotFound
	res.StatusText = statusText[statusNotFound]
	return res
}

func validProto(proto string) bool {
	if proto != PROTO {
		return false
	}
	return true
}
func ReadRequest(br *bufio.Reader) (req *Request, bytes bool, err error) {
	req = &Request{}

	req.init()

	var line string

	line, err = ReadLine(br)
	if errors.Is(err, io.EOF) {
		return nil, false, err
	}
	if err != nil {
		return req, false, myError("Error while parsing request ", err.Error())
	}
	req.Method, req.URL, req.Proto, err = parseRequestLine(line)
	if err != nil {
		fmt.Print("Malformed start line error: ", err.Error())
		return nil, true, myError("malformed start line", err.Error())
	}

	if !validMethod(req.Method) {
		return nil, true, myError("invalid method", req.Method)
	}
	if !validProto(req.Proto) {
		return nil, true, myError("Protocol is wrong. Expected HTTP/1.1, got: ", req.Proto)
	}
	for {
		line, err := ReadLine(br)
		if err != nil {
			return nil, true, err
		}
		if line == "" {
			// This marks header end
			break
		}
		if !strings.Contains(line, ":") {
			return req, true, myError("InvalidHeader: Header does not contain colon", line)
		} else {
			fields := strings.SplitN(line, ":", 2)
			if len(fields) != 2 {
				return req, true, myError("InvalidHeader: Header does not contain two colon-separated values %v", line)
			}
			key := CanonicalHeaderKey(strings.TrimSpace(fields[0]))
			if strings.Contains(key, " ") {
				return req, true, myError("InvalidHeader: key in header has whitespace", line)
			}
			value := strings.TrimSpace(fields[1])
			if strings.Contains(value, " ") {
				return req, true, myError("InvalidHeader: value in header has whitespace", line)
			}
			req.Headers[key] = strings.ToLower(value)
		}
		// fmt.Println("Read line from request", line)
	}
	err = req.processHeader()
	return req, true, err
}

// parseRequestLine parses "GET /foo HTTP/1.1" into its individual parts.
func parseRequestLine(line string) (string, string, string, error) {
	fields := strings.SplitN(line, " ", 3)
	if len(fields) != 3 {
		return "", "", "", fmt.Errorf("could not parse the request line, got fields %v", fields)
	}
	return fields[0], fields[1], fields[2], nil
}

func validMethod(method string) bool {
	return method == "GET"
}

func myError(what, val string) error {
	return fmt.Errorf("%s %q", what, val)
}

func (s *Server) parseAndGenerateResponse(req *Request, res *Response) error {
	// res.Request = req
	if req.Close {
		res.Headers[CONNECTION] = "close"
	}
	if _, ok := s.VirtualHosts[req.Host]; !ok {
		res.StatusCode = statusBadRequest
		res.StatusText = statusText[statusBadRequest]
		res.Headers[CONNECTION] = "close"
		return myError("HostmyError: Host not present in DocRoot. Host: ", req.Host)
	}

	if strings.HasSuffix(req.URL, "/") {
		req.URL += "index.html"
	}
	filelocation := s.VirtualHosts[req.Host] + req.URL

	filelocation = filepath.Clean(filelocation)

	info, err := os.Stat(filelocation)
	if !os.IsNotExist(err) {
		if info.IsDir() {
			filelocation = filelocation + "/index.html"
			info, err = os.Stat(filelocation)
		}
	} else {
		fmt.Println("Not exist error", filelocation)
		res = s.HandleNotFoundRequest()
		return myError("HostmyError: File Not Found. ", filelocation)
	}
	fmt.Printf("Filelocation is: %s\n", filelocation)
	res.Headers["Content-Length"] = fmt.Sprint(info.Size())
	res.Headers["Last-Modified"] = fmt.Sprintf(FormatTime(info.ModTime()))
	res.Headers["Content-Type"] = MIMETypeByExtension(filelocation[strings.LastIndex(filelocation, "."):])
	res.FilePath = filelocation
	body, _ := os.ReadFile(filelocation)
	res.Body = string(body)

	return nil
}

// ReadLine reads a single line ending with "\r\n" from br,
// striping the "\r\n" line end from the returned string.
// If any error occurs, data read before the error is also returned.
// You might find this function useful in parsing requests.
func ReadLine(br *bufio.Reader) (string, error) {
	var line string
	for {
		s, err := br.ReadString('\n')
		line += s
		// Return the error
		if err != nil {
			return line, err
		}
		// Return the line when reaching line end
		if strings.HasSuffix(line, "\r\n") {
			// Striping the line end
			line = line[:len(line)-2]
			return line, nil
		}
	}
}
