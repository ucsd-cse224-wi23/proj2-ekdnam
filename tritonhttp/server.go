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

	HOST       = "host"
	CONNECTION = "connection"
	DATE       = "Date"
	PROTO      = "HTTP/1.1"

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

// func (s *Server) ValidateServerSetup() error {
// 	// Validating the doc root of the server
// 	fi, err := os.Stat(s.DocRoot)

// 	if os.IsNotExist(err) {
// 		return err
// 	}

// 	if !fi.IsDir() {
// 		return fmt.Errorf("doc root %q is not a directory", s.DocRoot)
// 	}

// 	return nil
// }

func prettyPrintReq(request *Request) {
	empJSON, err := json.MarshalIndent(*request, "", "  ")
	if err != nil {
		log.Fatalf(err.Error())
	}
	fmt.Printf("Request %s\n", string(empJSON))
}

func prettyPrintRes(response *Response) {
	empJSON, err := json.MarshalIndent(*response, "", "  ")
	if err != nil {
		log.Fatalf(err.Error())
	}
	fmt.Printf("Response %s\n", string(empJSON))
}

// HandleConnection reads requests from the accepted conn and handles them.
func (s *Server) HandleConnection(conn net.Conn) {
	br := bufio.NewReader(conn)
	for {
		// Set timeout
		if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			log.Printf("Failed to set timeout for connection %v", conn)
			_ = conn.Close()
			return
		}

		// Read next request from the client
		req, err := ReadRequest(br)
		// Handle EOF
		if errors.Is(err, io.EOF) {
			log.Printf("Connection closed by %v", conn.RemoteAddr())
			_ = conn.Close()
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
		// timeout in this application means we just close the connection
		// Note : proj3 might require you to do a bit more here
		if err, ok := err.(net.Error); ok && err.Timeout() {
			log.Printf("Connection to %v timed out", conn.RemoteAddr())
			_ = conn.Close()
			return
		}

		err = req.processHeader()

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
		// if req.Close {
		// 	fmt.Print("`Connection: close` header encountered\nClosing connection\n")
		// 	res := s.HandleCloseRequest()
		// 	prettyPrintRes(res)
		// 	err = res.Write(conn)
		// 	if err != nil {
		// 		fmt.Println(err)
		// 	}
		// 	conn.Close()
		// 	return
		// }
		// Handle the request which is not a GET and immediately close the connection and return
		if err != nil {
			log.Printf("Handle bad request for error: %v", err)
			res := &Response{}
			res.HandleBadRequest()
			prettyPrintRes(res)
			_ = res.Write(conn)
			_ = conn.Close()
			return
		}
		// Handle good request
		// log.Printf("Handle good request: %v", string(empJSON))

		res := s.HandleGoodRequest()
		err = s.parseAndGenerateResponse(req, res)
		prettyPrintRes(res)
		// 404 error
		if err != nil {
			res := s.HandleNotFoundRequest()
			fmt.Println("404 error; Closing connection")
			_ = res.Write(conn)
			_ = conn.Close()
			return
		}
		err = res.Write(conn)
		if err != nil {
			fmt.Println(err)
		}
		if req.Close {
			err = res.Write(conn)
			if err != nil {
				fmt.Println(err)
			}
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
	res.HandleOK()
	return res
}

func (s *Server) HandleBadRequest() (res *Response) {
	res = &Response{}
	res.HandleBadRequest()
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
	res.HandleNotFound()
	return res
}

func validProto(proto string) bool {
	if proto != PROTO {
		return false
	}
	return true
}
func ReadRequest(br *bufio.Reader) (req *Request, err error) {
	req = &Request{}

	req.init()

	// Read start line
	// line, err := ReadLine(br)
	// if err != nil {
	// 	return nil, err
	// }
	var line string
	// for {
	// 	line, err = ReadLine(br)
	// 	if errors.Is(err, io.EOF) {
	// 		return nil, err
	// 	}
	// 	if err != nil {
	// 		return req, invalidHeaderError("Error while parsing request ", err.Error())
	// 	}
	// 	if line != "" {
	// 		break
	// 	}
	// }
	line, err = ReadLine(br)
	if errors.Is(err, io.EOF) {
		return nil, err
	}
	if err != nil {
		return req, invalidHeaderError("Error while parsing request ", err.Error())
	}
	req.Method, req.URL, req.Proto, err = parseRequestLine(line)
	if err != nil {
		fmt.Print("Malformed start line error: ", err.Error())
		return nil, badStringError("malformed start line", err.Error())
	}

	if !validMethod(req.Method) {
		return nil, badStringError("invalid method", req.Method)
	}
	if !validProto(req.Proto) {
		return nil, invalidHeaderError("Protocol is wrong. Expected HTTP/1.1, got: ", req.Proto)
	}
	for {
		line, err := ReadLine(br)
		if err != nil {
			return nil, err
		}
		if line == "" {
			// This marks header end
			break
		}
		if !strings.Contains(line, ":") {
			return req, invalidHeaderError("InvalidHeader: Header does not contain colon", line)
		} else {
			fields := strings.SplitN(line, ":", 2)
			if len(fields) != 2 {
				return req, invalidHeaderFieldQuantityMismatchError("InvalidHeader: Header does not contain two colon-separated values %v", line)
			}
			key := strings.TrimSpace(fields[0])
			if strings.Contains(key, " ") {
				return req, invalidHeaderError("InvalidHeader: key in header has whitespace", line)
			}
			value := strings.TrimSpace(fields[1])
			if strings.Contains(value, " ") {
				return req, invalidHeaderError("InvalidHeader: value in header has whitespace", line)
			}
			req.Headers[strings.ToLower(key)] = strings.ToLower(value)
		}
		// fmt.Println("Read line from request", line)
	}

	return req, nil
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

func badStringError(what, val string) error {
	return fmt.Errorf("%s %q", what, val)
}

func notFoundError(what, val string) error {
	return fmt.Errorf("%s %q", what, val)
}

func invalidHeaderError(what, val string) error {
	return fmt.Errorf("%s %q", what, val)
}

func invalidHeaderFieldQuantityMismatchError(what, val string) error {
	return fmt.Errorf("%s %q", what, val)
}

func badError(what, val string) error {
	return fmt.Errorf("%s %q", what, val)
}

func (s *Server) parseAndGenerateResponse(req *Request, res *Response) error {
	res.Request = req

	if _, ok := s.VirtualHosts[req.Host]; !ok {
		return notFoundError("HostNotFoundError: Host not present in DocRoot. Host: ", req.Host)
	}

	filelocation := fmt.Sprint(s.VirtualHosts[req.Host], req.URL)

	filelocation = filepath.Clean(filelocation)

	info, err := os.Stat(filelocation)
	if !os.IsNotExist(err) {
		if info.IsDir() {
			filelocation = filelocation + "/index.html"
			info, err = os.Stat(filelocation)
		}
	}
	if os.IsNotExist(err) {
		fmt.Println("Not exist error", filelocation)
		res = s.HandleNotFoundRequest()
		return notFoundError("HostNotFoundError: File Not Found. ", filelocation)
	} else if err != nil {
		res = s.HandleBadRequest()
		return badError("unexpected error occurred: ", err.Error())
	}
	fmt.Printf("Filelocation is: %s\n", filelocation)
	res.Headers["Content-Length"] = fmt.Sprint(info.Size())
	res.Headers["Last-Modified"] = fmt.Sprintf(FormatTime(info.ModTime()))
	res.Headers["Content-Type"] = MIMETypeByExtension(filepath.Ext(filelocation))
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
