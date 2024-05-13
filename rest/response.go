package rest

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
)

// Raw is response's raw data
type Raw []byte

// Response is a http response wrapper
type Response struct {
	*http.Response
}

func NewResponse(response *http.Response) *Response {
	return &Response{
		Response: response,
	}
}

// SuccessDecider decide should we decode the response or not
type SuccessDecider func(*http.Response) bool

// DecodeOnSuccess decide that we should decode on success response (http code 2xx)
func DecodeOnSuccess(resp *http.Response) bool {
	return 200 <= resp.StatusCode && resp.StatusCode <= 299
}

// ResponseDecoder decodes http responses into struct values.
type ResponseDecoder interface {
	// Decode decodes the response into the value pointed to by v.
	Decode(resp *http.Response, v interface{}) error
}

// jsonDecoder decodes http response JSON into a JSON-tagged struct value.
type jsonDecoder struct {
}

// Decode decodes the Response Body into the value pointed to by v.
// Caller must provide a non-nil v and close the resp.Body.
func (d jsonDecoder) Decode(resp *http.Response, v interface{}) error {
	return json.NewDecoder(resp.Body).Decode(v)
}

type xmlDecoder struct {
}

func (d xmlDecoder) Decode(resp *http.Response, v interface{}) error {
	return xml.NewDecoder(resp.Body).Decode(v)
}
