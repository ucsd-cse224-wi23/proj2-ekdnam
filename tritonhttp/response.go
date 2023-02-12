package tritonhttp

import (
	"bufio"
	"fmt"
	"io"
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

// HandleOK prepares res to be a 200 OK response
// ready to be written back to client.
func (res *Response) HandleOK() {
	res.init()
	res.StatusCode = statusOK
	res.StatusText = statusText[statusOK]
}

func (res *Response) HandleClose() {
	res.init()
	res.StatusCode = statusOK
	res.StatusText = statusText[statusOK]
	res.FilePath = ""
	res.Headers[CONNECTION] = "close"
}

// HandleBadRequest prepares res to be a 405 Method Not allowed response
func (res *Response) HandleBadRequest() {
	res.init()
	res.StatusCode = statusBadRequest
	res.StatusText = statusText[statusBadRequest]
	res.FilePath = ""
	res.Headers[CONNECTION] = "close"
}

func (res *Response) init() {
	res.Proto = responseProto
	res.Headers = make(map[string]string)
	res.Body = ""
	res.Headers[DATE] = FormatTime(time.Now())
}

func (res *Response) getStatusLine() string {
	return fmt.Sprintf("%v %v %v\r\n", res.Proto, res.StatusCode, statusText[res.StatusCode])
}

func (res *Response) Write(w io.Writer) error {
	bw := bufio.NewWriterSize(w, 4096)

	statusLine := res.getStatusLine()
	headers := res.generateResponseHeaders()
	_, err := w.Write([]byte(statusLine + headers + "\r\n"))
	if err != nil {
		return err
	}
	if res.StatusCode == 200 {
		_, err := w.Write([]byte(res.Body))
		if err != nil {
			return err
		}
	}

	if err := bw.Flush(); err != nil {
		return err
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
	for _, k := range keys {
		headerValue, ok := res.Headers[k]
		if ok {
			line += fmt.Sprintf(k + ": " + headerValue + "\r\n")
		}
	}
	return line
}

func (res *Response) HandleNotFound() {
	res.init()
	res.StatusCode = statusNotFound
	res.Headers[CONNECTION] = "close"
}
