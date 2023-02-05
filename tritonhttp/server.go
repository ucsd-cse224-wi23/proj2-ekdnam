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

func (s *Server) ListenAndServe() error {
	// Validate the configuration of the server
	if err := s.ValidateServerSetup(); err != nil {
		return fmt.Errorf("server is not setup correctly %v", err)
	}
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

func (s *Server) ValidateServerSetup() error {
	// Validating the doc root of the server
	fi, err := os.Stat(s.DocRoot)

	if os.IsNotExist(err) {
		return err
	}

	if !fi.IsDir() {
		return fmt.Errorf("doc root %q is not a directory", s.DocRoot)
	}

	return nil
}

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
		if req.Close {
			fmt.Print("`Connection: close` header encountered\nClosing connection\n")
			res := s.HandleCloseRequest()
			err = res.Write(conn)
			if err != nil {
				fmt.Println(err)
			}
			conn.Close()
			return
		}
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
		prettyPrintRes(res)
		err = res.Write(conn)
		if err != nil {
			fmt.Println(err)
		}

		// We'll never close the connection and handle as many requests for this connection and pass on this
		// responsibility to the timeout mechanism
	}
}

func (req *Request) processHeader() (err error) {
	if req.URL[0] != '/' {
		return invalidHeaderError("InvalidHeader: Request URL should start with `/`, but URL is ", req.URL)
	}
	_, ok := req.Headers[HOST]
	if !ok {
		b, err := json.Marshal(req.Headers)
		if err != nil {
			return invalidHeaderError("InvalidHeader: Does contain `host` field & header cannot be converted to JSON", "")
		}
		return invalidHeaderError("InvalidHeader: Does not contain `host` field", string(b))
	}
	req.Host = req.Headers[HOST]
	_, ok = req.Headers[CONNECTION]
	if ok {
		val := req.Headers[CONNECTION]
		if val == "close" {
			req.Close = true
		} else {
			return invalidHeaderError("InvalidHeader: `Connection` key in Header has invalid value. Allowed: close, actual: ", req.Headers[CONNECTION])
		}
	}

	return nil
}

func (s *Server) HandleCloseRequest() (res *Response) {
	res = &Response{}
	res.HandleOK(true)
	res.FilePath = filepath.Join(s.DocRoot, "hello-world.txt")

	return res
}

func (s *Server) HandleGoodRequest() (res *Response) {
	res = &Response{}
	res.HandleOK(false)
	res.FilePath = filepath.Join(s.DocRoot, "hello-world.txt")

	return res
}

func getDate() (int, time.Month, int) {
	now := time.Now()
	return now.Date()
}

// HandleOK prepares res to be a 200 OK response
// ready to be written back to client.
func (res *Response) HandleOK(connClose bool) {
	res.init()
	res.StatusCode = statusOK
}

// HandleBadRequest prepares res to be a 405 Method Not allowed response
func (res *Response) HandleBadRequest() {
	res.init()
	res.StatusCode = statusBadRequest
	res.FilePath = ""
}

func (res *Response) init() {
	res.Proto = responseProto
	res.Headers = make(map[string]string)

	res.Headers[DATE] = FormatTime(time.Now())
}

func ReadRequest(br *bufio.Reader) (req *Request, err error) {
	req = &Request{}

	req.init()

	// Read start line
	line, err := ReadLine(br)
	if err != nil {
		return nil, err
	}

	req.Method, req.URL, req.Proto, err = parseRequestLine(line)
	if err != nil {
		return nil, badStringError("malformed start line", line)
	}

	if !validMethod(req.Method) {
		return nil, badStringError("invalid method", req.Method)
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
		fmt.Println("Read line from request", line)
	}

	return req, nil
}

func (req *Request) init() {
	req.Headers = make(map[string]string)
	req.Close = false
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

func invalidHeaderError(what, val string) error {
	return fmt.Errorf("%s %q", what, val)
}

func invalidHeaderFieldQuantityMismatchError(what, val string) error {
	return fmt.Errorf("%s %q", what, val)
}

func (res *Response) generateResponseHeaders() string {
	line := ""
	for key, value := range res.Headers {
		line += key + ": " + value + "\r\n"
	}
	// line += "\r\n"
	return line
}

func (s *Server) parseAndGenerateResponse(req *Request, res *Response) {
	res.Request = req

	host := req.Host
	url := req.URL

	filelocation := s.DocRoot + "/" + host + url

	info, err := os.Stat(filelocation)
	if os.IsNotExist(err) {
		fmt.Printf(err.Error())
	}
	req.Headers["Content-Length"] = fmt.Sprint(info.Size())
	req.Headers["Last-Modified"] = fmt.Sprintf(FormatTime(info.ModTime()))
	req.Headers["Content-Type"] = MIMETypeByExtension(filepath.Ext(filelocation))

}

func (res *Response) Write(w io.Writer) error {
	bw := bufio.NewWriter(w)

	statusLine := fmt.Sprintf("%v %v %v\r\n", res.Proto, res.StatusCode, statusText[res.StatusCode])
	if _, err := bw.WriteString(statusLine + res.generateResponseHeaders()); err != nil {
		return err
	}

	if err := bw.Flush(); err != nil {
		return err
	}
	return nil
}
