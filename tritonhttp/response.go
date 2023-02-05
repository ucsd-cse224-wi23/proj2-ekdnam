package tritonhttp

import (
	"bufio"
	"fmt"
	"io"
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

func (res *Response) generateResponseHeaders() string {
	line := ""
	for key, value := range res.Headers {
		line += key + ": " + value + "\r\n"
	}
	// line += "\r\n"
	return line
}

func (res *Response) HandleNotFound() {
	res.init()
	res.StatusCode = statusNotFound
}
