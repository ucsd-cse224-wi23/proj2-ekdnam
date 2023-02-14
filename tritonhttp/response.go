package tritonhttp

import (
	"bufio"
	"fmt"
	"io"
	"mime"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Response struct {
	Proto      string // e.g. "HTTP/1.1"
	StatusCode int    // e.g. 200
	StatusText string // e.g. "OK"

	// Headers stores all headers to write to the response.
	Headers map[string]string

	Date string

	// Request is the valid request that leads to this response.
	// It could be nil for responses not resulting from a valid request.
	// Hint: you might need this to handle the "Connection: Close" requirement
	Request *Request

	// FilePath is the local path to the file to serve.
	// It could be "", which means there is no file to serve.
	ContentLength int
	LastModified  string
	ContentType   string
	Body          string
	Connection    bool
}

func (res *Response) Write(w io.Writer, conn net.Conn) error {
	bw := bufio.NewWriter(w)
	response := res.ToString()
	fmt.Println("Sending response")
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
	}
	return nil
}

func (res *Response) init() {
	res.Date = FormatTime(time.Now())

	res.Proto = PROTO

	res.ContentType = CONTENTTYPE
	res.ContentLength = -1
}

func (res *Response) HandleGoodRequest(req *Request, virtualHosts map[string]string) {

	res.Request = req

	res.init()
	if req.Close {
		res.Connection = true
	}
	filelocation := ""
	if strings.HasSuffix(req.URL, "/") {
		filelocation = req.URL + "index.html"
	} else {
		filelocation = req.URL
	}

	docroot, ok := virtualHosts[req.Host]

	res.StatusCode = statusNotFound
	res.StatusText = statusText[statusNotFound]
	wrong := false
	if ok {
		filelocfinal := docroot + filelocation
		filelocfinal = filepath.Clean(filelocfinal)

		if strings.Contains("../", filelocfinal) {
			wrong = true
		}

		info, err := os.Stat(filelocfinal)

		if os.IsNotExist(err) {
			fmt.Println(myError("FileNotFoundError: ", filelocfinal))
			wrong = true
		} else if info.IsDir() {
			wrong = true
		} else {
			body, err := os.ReadFile(filelocfinal)
			if err != nil {
				fmt.Println(myError("ReadError: ", err.Error()))
				res.Connection = true
				return
			}
			res.ContentLength = int(info.Size())
			res.LastModified = FormatTime(info.ModTime())
			res.Body = string(body)
			res.ContentType = mime.TypeByExtension(filepath.Ext(filelocfinal))
		}

	} else {
		res.StatusCode = statusBadRequest
		res.StatusText = statusText[statusBadRequest]
		fmt.Println(badStringError("Host not present: ", req.Host))
		return
	}
	if !wrong {
		res.StatusCode = statusOK
		res.StatusText = statusText[statusOK]
	}
}

func (res *Response) HandleBadRequest(req *Request) {

	res.Request = req
	res.Date = FormatTime(time.Now())
	res.Proto = PROTO

	res.ContentLength = -1
	res.ContentType = CONTENTTYPE

	res.StatusCode = statusBadRequest
	res.StatusText = statusText[statusBadRequest]

	res.Connection = true

	return
}
