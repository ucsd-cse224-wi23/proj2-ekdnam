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
		fmt.Println("BEGINNING OF FOR")

		// Set timeout
		if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			log.Printf("Failed to set timeout for connection %v", conn)
			_ = conn.Close()
			break
		}

		// Read next request from the client
		req, bytesRead, err := ReadRequest(br)

		if errors.Is(err, io.EOF) {
			log.Printf("EOF")
			continue
		}

		if err, ok := err.(net.Error); ok && err.Timeout() {
			if !bytesRead {
				log.Printf("Connection to %v timed out", conn.RemoteAddr())
				_ = conn.Close()
			} else {
				res := s.HandleBadRequest(req)
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

			res := s.HandleBadRequest(req)
			err = res.Write(conn, conn)
			if err != nil {
				fmt.Println(err)
			}
			_ = conn.Close()

		} else {
			// Handle good request
			log.Printf("Handle good request: %v", req)
			res := s.HandleGoodRequest(req)
			err = res.Write(conn, conn)
			if err != nil {
				fmt.Println(err)
				return
			}
		}

		// fmt.Println("END OF FOR")
	}
}

func ReadRequest(br *bufio.Reader) (req *Request, bytes bool, err error) {
	req = &Request{}

	req.init()

	var line string

	line, err = ReadLine(br)

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
	if req.URL[0] != '/' {
		return nil, true, myError("InvalidHeader: Request URL should start with `/`, but URL is ", req.URL)
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
	val, ok := req.Headers[CONNECTION]
	if ok {
		if strings.ToLower(val) == "close" {
			req.Close = true
		}
	}
	_, ok = req.Headers[HOST]
	if !ok {
		return req, true, myError("InvalidHeader: Does not contain `host` field", "")
	}
	req.Host = req.Headers[HOST]

	return req, true, nil
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

func validProto(method string) bool {
	return method == "HTTP/1.1"
}

func badStringError(what, val string) error {
	return fmt.Errorf("%s %q", what, val)
}

func (res *Response) Write(w io.Writer, conn net.Conn) error {
	bw := bufio.NewWriter(w)
	response := convertRespToString(res)
	fmt.Println("Giving Response")
	if _, err := bw.WriteString(response); err != nil {
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

func (s *Server) HandleGoodRequest(req *Request) (res *Response) {
	res = &Response{}

	res.Request = req
	res.Date = FormatTime(time.Now())

	res.Proto = PROTO

	res.ContentType = CONTENTTYPE
	res.ContentLength = -1

	var web_file_dir = ""
	if strings.HasSuffix(req.URL, "/") {
		web_file_dir = req.URL + "index.html"
	} else {
		web_file_dir = req.URL
	}

	// fmt.Println(s.VirtualHosts)
	// fmt.Println(req.Host)
	base_dir, ok := s.VirtualHosts[req.Host]
	// base_dir = strings.Replace(base_dir, "../", "", -1)

	res.StatusCode = statusNotFound
	noOK := false
	if ok {
		// fmt.Println("BASE DIR: ", base_dir, web_file_dir)
		fmt.Println("base dir: ", base_dir)
		fmt.Println("web file dir: ", web_file_dir)
		fullPath := base_dir + web_file_dir
		fmt.Println("full path requested: ", fullPath)
		fullPath = filepath.Clean(fullPath)
		fmt.Println("full path requested post cleaning: ", fullPath)

		if strings.Contains("../", fullPath) {
			fmt.Println("../ detected")
			// res.Connection = true
			// return res
			noOK = true
		}

		fi, err := os.Stat(fullPath)

		if os.IsNotExist(err) {
			fmt.Println("Is Not Exist Error")
			// res.Connection = true
			// return res
			noOK = true
		} else if fi.IsDir() {
			fmt.Println("Is Dir Error")
			// res.Connection = true
			// return res
			noOK = true
		} else {
			content, err := os.ReadFile(fullPath)
			if err != nil {
				fmt.Println("File Read Error")
				res.Connection = true
				return res
			}
			res.ContentLength = int(fi.Size())
			res.LastModified = FormatTime(fi.ModTime())
			res.Body = string(content)
			res.ContentType = strings.Split(MIMETypeByExtension(fullPath[strings.LastIndex(fullPath, "."):]), ";")[0]
		}

	} else {
		res.StatusCode = statusBadRequest
		fmt.Println("No OK Error")
		// res.Connection = true
		return res
	}

	if !noOK {
		res.StatusCode = statusOK
	}

	if req.Close {
		res.Connection = true
	}

	return res
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

func (s *Server) HandleBadRequest(req *Request) (res *Response) {
	res = &Response{}

	res.Request = req
	res.Date = FormatTime(time.Now())
	res.Proto = PROTO

	res.ContentLength = -1
	res.ContentType = CONTENTTYPE

	res.StatusCode = statusBadRequest

	res.Connection = true

	return res
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
