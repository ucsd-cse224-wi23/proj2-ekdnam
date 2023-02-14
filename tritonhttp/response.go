package tritonhttp

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"sort"
	"time"
)

type Response struct {
	Proto      string // e.g. "HTTP/1.1"
	StatusCode int    // e.g. 200
	StatusText string // e.g. "OK"

	// Headers stores all headers to write to the response.
	Headers map[string]string

	// Request is the valid request that leads to this response.
	// It could be nil for responses not resulting from a valid request.
	// Hint: you might need this to handle the "Connection: Close" requirement
	Request *Request

	// FilePath is the local path to the file to serve.
	// It could be "", which means there is no file to serve.
	FilePath string

	// Response body will contain response as a HTML
	Body string
}

func (res *Response) init(req *Request) {
	res.Proto = responseProto
	res.Headers = make(map[string]string)
	res.Body = ""
	res.Headers[DATE] = FormatTime(time.Now())
	if req.Close {
		res.Headers["Connection"] = "close"
	}
}

func (res *Response) getStatusLine() string {
	return fmt.Sprintf("%v %v %v\r\n", res.Proto, res.StatusCode, statusText[res.StatusCode])
}

func (res *Response) Write(w io.Writer, conn net.Conn) error {
	bw := bufio.NewWriterSize(w, MAXSIZE)

	statusLine := res.getStatusLine()
	headers := res.generateResponseHeaders()
	fmt.Printf("Headers: %s", headers)
	_, err := w.Write([]byte(statusLine + headers + "\r\n"))
	if err != nil {
		return err
	}
	if res.StatusCode == 200 {
		_, err := w.Write([]byte(res.Body))
		if err != nil {
			_ = conn.Close()
			return err
		}
	}

	if err := bw.Flush(); err != nil {
		_ = conn.Close()
		return err
	}
	if res.Headers[CONNECTION] == "close" {
		err = conn.Close()
		if err != nil {
			return myError("error while closing connection, ", err.Error())
		}
	}
	return nil
}

func (res *Response) generateResponseHeaders() string {
	line := ""
	idx := 0
	keys := make([]string, len(res.Headers))
	for k := range res.Headers {
		keys[idx] = k
		idx += 1
	}
	sort.Strings(keys)
	fmt.Println("Header keys: ", keys)
	// fmt.Println("Headers from response: ", res.Headers)
	for _, k := range keys {
		headerValue, ok := res.Headers[k]
		if ok {
			line += fmt.Sprintf(k + ": " + headerValue + "\r\n")
		}
	}
	return line
}

func (res *Response) HandleNotFound(req *Request) {
	res.init(req)
	res.StatusCode = statusNotFound
	// res.Headers[CONNECTION] = "close"
}
