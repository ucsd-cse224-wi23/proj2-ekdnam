package tritonhttp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	statusOK               = 200
	statusBadRequest       = 400
	statusNotFound         = 404
	statusMethodNotAllowed = 405

	HOST        = "Host"
	CONNECTION  = "Connection"
	DATE        = "Date"
	PROTO       = "HTTP/1.1"
	MAXSIZE     = 10000
	CONTENTTYPE = "text/html"
	CRLF        = "\r\n"
	// LAYOUT = "01 02 2006 15:04:05"
)

var statusText = map[int]string{
	statusOK:               "OK",
	statusMethodNotAllowed: "Method Not Allowed",
	statusNotFound:         "Not Found",
	statusBadRequest:       "Bad Request",
}

type Server struct {
	// Addr specifies the TCP address for the server to listen on,
	// in the form "host:port". It shall be passed to net.Listen()
	// during ListenAndServe().
	Addr string // e.g. ":0"
	// DocRoot string
	// VirtualHosts contains a mapping from host name to the docRoot path
	// (i.e. the path to the directory to serve static files from) for
	// all virtual hosts that this server supports
	VirtualHosts map[string]string
}

func myError(what, val string) error {
	return fmt.Errorf("%s %q", what, val)
}

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
		if strings.HasSuffix(line, CRLF) {
			// Striping the line end
			line = line[:len(line)-2]
			return line, nil
		}
	}
}

// ListenAndServe listens on the TCP network address s.Addr and then
// handles requests on incoming connections.
func (s *Server) ListenAndServe() error {

	// Hint: Validate all docRoots

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

	// Hint: create your listen socket and spawn off goroutines per incoming client
	// panic("todo")
}

func (s *Server) ValidateServerSetup() error {
	// Validating the doc root of the server

	for website, path := range s.VirtualHosts {
		// fmt.Println("Key:", key, "=>", "Element:", element)
		fi, err := os.Stat(path)

		if os.IsNotExist(err) {
			return err
		}

		if !fi.IsDir() {
			return fmt.Errorf("doc root %q is not a directory for %q", path, website)
		}
	}

	return nil
}

// HandleConnection reads requests from the accepted conn and handles them.
func (s *Server) HandleConnection(conn net.Conn) {
	br := bufio.NewReader(conn)
	for {
		res := &Response{}
		// Set timeout
		if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			log.Printf("Failed to set timeout for connection %v", conn)
			_ = conn.Close()
			break
		}
		// Read next request from the client
		req, bytes, err := ReadRequest(br)

		if errors.Is(err, io.EOF) {
			log.Printf("Connection: end of file\n")
			continue
		}
		if err, ok := err.(net.Error); ok && err.Timeout() {
			if !bytes {
				log.Printf("Connection to %v timed out", conn.RemoteAddr())
				_ = conn.Close()
			} else {
				res.HandleBadRequest(req)
				err := res.Write(conn, conn)
				if err != nil {
					fmt.Println(err)
				}
				_ = conn.Close()
			}
			return
		}
		if err != nil {
			log.Printf("Handle bad request for error: %v", err)
			res.HandleBadRequest(req)
			err = res.Write(conn, conn)
			if err != nil {
				fmt.Println(err)
			}
			_ = conn.Close()

		} else {
			// Handle good request
			log.Printf("Handle good request: %v", req)
			res.HandleGoodRequest(req, s.VirtualHosts)
			err = res.Write(conn, conn)
			if err != nil {
				fmt.Println(err)
				return
			}
		}
	}
}

func ReadRequest(br *bufio.Reader) (req *Request, bytesRead bool, err error) {
	req = &Request{}
	req.Close = false
	line, err := ReadLine(br)
	if err != nil {
		return nil, false, err
	}
	fmt.Print(line + "\n")
	req.Method, req.URL, req.Proto, err = parseRequestLine(line, req)
	if err != nil {
		return nil, true, badStringError("malformed start line", line)
	}

	if !validMethod(req.Method) {
		return nil, true, badStringError("invalid method", req.Method)
	}
	if !validProto(req.Proto) {
		return nil, true, badStringError("invalid Proto", req.Proto)
	}
	if req.URL[0] != '/' {
		return nil, true, badStringError("invalid URL", req.URL)
	}
	host := false
	for {
		line, err := ReadLine(br)
		fmt.Println("req line: ", line)
		if err != nil {
			return nil, true, err
		}
		if line == "" {
			break
		}
		if !strings.Contains(line, ":") {
			return req, true, myError("InvalidHeader: Header does not contain colon", line)
		}
		fields := strings.SplitN(line, ": ", 2)
		if len(fields) != 2 {
			return nil, true, myError("InvalidHeader: Header does not contain two colon-separated values %v", line)
		}

		key := CanonicalHeaderKey(strings.TrimSpace(fields[0]))
		value := strings.TrimSpace(fields[1])
		if strings.Contains(key, " ") {
			return req, true, myError("InvalidHeader: key in header has whitespace", line)
		}
		if strings.Contains(value, " ") {
			return req, true, myError("InvalidHeader: value in header has whitespace", line)
		}
		if key == "Host" {
			if value == "close" {
				req.Close = true
			}
		} else if key == "Connection" {
			req.Host = value
			host = true
		}
	}

	if !host {
		return nil, true, myError("HostNotPresentError ", "")
	}
	return req, true, nil
}

// parseRequestLine parses "GET /foo HTTP/1.1" into its individual parts.
func parseRequestLine(line string, req *Request) (string, string, string, error) {
	fields := strings.SplitN(line, " ", 3)
	if len(fields) != 3 {
		return "", "", "", fmt.Errorf("could not parse the request line, got fields %v", fields)
	}

	return fields[0], fields[1], fields[2], nil
}

func validMethod(method string) bool {
	return method == "GET"
}

func validProto(proto string) bool {
	return proto == "HTTP/1.1"
}

func badStringError(what, val string) error {
	return fmt.Errorf("%s %q", what, val)
}

func (res *Response) addInitialLine() string {
	return fmt.Sprintf("%s %s %s%s", res.Proto, strconv.Itoa(res.StatusCode), res.StatusText, CRLF)
}
func (res *Response) addHeaders() string {
	var headers string
	if res.Connection == true {
		headers += fmt.Sprintf("Connection: close%s", CRLF)
	}
	if !(res.ContentLength < 0) {
		headers += fmt.Sprintf("Content-Length: %s%s", strconv.Itoa(res.ContentLength), CRLF)
		headers += fmt.Sprintf("Content-Type: %s%s", res.ContentType, CRLF)
	}
	headers += fmt.Sprintf("Date: %s%s", res.Date, CRLF)
	if res.ContentLength >= 0 {
		headers += fmt.Sprintf("Last-Modified: %s%s", res.LastModified, CRLF)
	}

	headers += CRLF
	headers += res.Body
	return headers

}

func (res *Response) ToString() string {
	var OUT string
	OUT += res.addInitialLine()

	OUT += res.addHeaders()

	OUT += CRLF
	OUT += res.Body
	return OUT
}
