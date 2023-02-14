package tritonhttp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
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
		if strings.HasSuffix(line, "\r\n") {
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
			log.Printf("End of file encountered")
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

		// Handle the request which is not a GET and immediately close the connection and return
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

	fmt.Println("Request Line: ", line)

	req.Method, err = parseRequestLine(line, req)

	if err != nil {
		return nil, true, badStringError("malformed start line", line)
	}

	if !validMethod(req.Method) {
		return nil, true, badStringError("invalid method", req.Method)
	}
	if !validProto(req.Proto) {
		return nil, true, badStringError("invalid Proto", req.Method)
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
			req.Host = value
			host = true
		} else if key == "Connection" {
			if value == "close" {
				req.Close = true
			}
		}
	}

	if !host {
		return nil, true, myError("HostNotPresentError ", "")
	}
	return req, true, nil
}

// parseRequestLine parses "GET /foo HTTP/1.1" into its individual parts.
func parseRequestLine(line string, req *Request) (string, error) {
	fields := strings.SplitN(line, " ", 3)
	if len(fields) != 3 {
		return "", fmt.Errorf("could not parse the request line, got fields %v", fields)
	}

	req.Method = fields[0]
	req.URL = fields[1]
	req.Proto = fields[2]

	return fields[0], nil
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

func (res *Response) Write(w io.Writer, conn net.Conn) error {
	bw := bufio.NewWriter(w)
	response := convertRespToString(res)
	fmt.Println("Giving Response")
	if _, err := bw.Write([]byte(response)); err != nil {
		_ = conn.Close()
		return err
	}
	if err := bw.Flush(); err != nil {
		_ = conn.Close()
		return err
	}
	if res.Connection {
		_ = conn.Close()
		// return errors.New("Connection Close Command")
	}
	return nil
}

func (res *Response) HandleGoodRequest(req *Request, virtualHosts map[string]string) {

	res.Request = req
	res.Date = FormatTime(time.Now())

	res.Proto = PROTO

	res.ContentType = CONTENTTYPE
	res.ContentLength = -1

	var filelocation = ""
	if strings.HasSuffix(req.URL, "/") {
		filelocation = req.URL + "index.html"
	} else {
		filelocation = req.URL
	}

	docroot, ok := virtualHosts[req.Host]

	res.StatusCode = statusNotFound
	noOK := false
	if ok {
		filelocfinal := docroot + filelocation
		fmt.Println("full path requested: ", filelocfinal)
		filelocfinal = filepath.Clean(filelocfinal)
		fmt.Println("full path requested post cleaning: ", filelocfinal)

		if strings.Contains("../", filelocfinal) {
			fmt.Println("../ detected")
			noOK = true
		}

		fi, err := os.Stat(filelocfinal)

		if os.IsNotExist(err) {
			fmt.Println("Is Not Exist Error")
			noOK = true
		} else if fi.IsDir() {
			fmt.Println("Is Dir Error")
			noOK = true
		} else {
			content, err := os.ReadFile(filelocfinal)
			if err != nil {
				fmt.Println("File Read Error")
				res.Connection = true
				return
			}
			res.ContentLength = int(fi.Size())
			res.LastModified = FormatTime(fi.ModTime())
			res.Body = string(content)
			res.ContentType = strings.Split(MIMETypeByExtension(filelocfinal[strings.LastIndex(filelocfinal, "."):]), ";")[0]
		}

	} else {
		res.StatusCode = statusBadRequest
		fmt.Println("No OK Error")
		return
	}

	if !noOK {
		res.StatusCode = statusOK
	}

	if req.Close {
		res.Connection = true
	}

	return
}

func convertRespToString(res *Response) string {
	var response string
	response += res.Proto + " " + strconv.Itoa(res.StatusCode) + " " + statusText[res.StatusCode] + "\r\n"

	if res.Connection {
		response += "Connection: " + "close" + "\r\n"
	}
	if res.ContentLength >= 0 {
		response += "Content-Length: " + strconv.Itoa(res.ContentLength) + "\r\n"
		response += "Content-Type: " + res.ContentType + "\r\n"
	}
	response += "Date: " + res.Date + "\r\n"

	if res.ContentLength >= 0 {
		response += "Last-Modified: " + res.LastModified + "\r\n"
	}

	response += "\r\n"
	response += res.Body
	return response
}

func (res *Response) HandleBadRequest(req *Request) {

	res.Request = req
	res.Date = FormatTime(time.Now())
	res.Proto = PROTO

	res.ContentLength = -1
	res.ContentType = CONTENTTYPE

	res.StatusCode = statusBadRequest

	res.Connection = true

	return
}
