package tritonhttp

import "encoding/json"

type Request struct {
	Method string // e.g. "GET"
	URL    string // e.g. "/path/to/a/file"
	Proto  string // e.g. "HTTP/1.1"

	// Headers stores the key-value HTTP headers
	Headers map[string]string

	Host  string // determine from the "Host" header
	Close bool   // determine from the "Connection" header
}

func (req *Request) init() {
	req.Headers = make(map[string]string)
	req.Close = false
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
