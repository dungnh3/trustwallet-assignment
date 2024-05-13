package rest

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"mime/multipart"
	"net/url"
	"strings"

	goquery "github.com/google/go-querystring/query"
)

// BodyProvider provides Body content for http.Request attachment.
type BodyProvider interface {
	// ContentType returns the Content-Type of the body.
	ContentType() string
	// Body returns the io.Reader body.
	Body() (io.Reader, error)
}

// BodyMultipartProvider provides Body Multipart content for http.Request attachment.
type BodyMultipartProvider interface {
	// Body returns the io.Reader body and Content-Type.
	Body() (io.Reader, string, error)
}

// bodyProvider provides the wrapped body value as a Body for reqests.
type bodyProvider struct {
	body io.Reader
}

func (p bodyProvider) ContentType() string {
	return ""
}

func (p bodyProvider) Body() (io.Reader, error) {
	return p.body, nil
}

// jsonBodyProvider encodes a JSON tagged struct value as a Body for requests.
// See https://golang.org/pkg/encoding/json/#MarshalIndent for details.
type jsonBodyProvider struct {
	payload interface{}
}

func (p jsonBodyProvider) ContentType() string {
	return jsonContentType
}

func (p jsonBodyProvider) Body() (io.Reader, error) {
	buf := &bytes.Buffer{}
	err := json.NewEncoder(buf).Encode(p.payload)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

// formBodyProvider encodes a url tagged struct value as Body for requests.
// See https://godoc.org/github.com/google/go-querystring/query for details.
type formBodyProvider struct {
	payload interface{}
}

func (p formBodyProvider) ContentType() string {
	return formContentType
}

func (p formBodyProvider) Body() (io.Reader, error) {
	values, err := goquery.Values(p.payload)
	if err != nil {
		return nil, err
	}
	return strings.NewReader(values.Encode()), nil
}

// formUrlEncoded, sometime formBodyProvider doesn't worked, so we manual encode

type formUrlEncodedProvider struct {
	values map[string]string
}

func (p formUrlEncodedProvider) ContentType() string {
	return formContentType
}

func (p formUrlEncodedProvider) Body() (io.Reader, error) {
	data := url.Values{}
	for k, v := range p.values {
		data.Set(k, v)
	}

	encodedData := data.Encode()
	return strings.NewReader(encodedData), nil
}

// multipartDataBodyProvider encodes a files upload
type multipartDataBodyProvider struct {
	payload     map[string]io.Reader
	filePayload map[string]io.Reader
}

func (p multipartDataBodyProvider) Body() (io.Reader, string, error) {
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)

	var err error
	for key, r := range p.payload {
		var fw io.Writer
		if x, ok := r.(io.Closer); ok {
			defer x.Close()
		}
		if fw, err = mw.CreateFormField(key); err != nil {
			return nil, "", err
		}
		if _, err = io.Copy(fw, r); err != nil {
			return nil, "", err
		}
	}

	for key, r := range p.filePayload {
		var fw io.Writer
		if x, ok := r.(io.Closer); ok {
			defer x.Close()
		}
		if fw, err = mw.CreateFormFile(key, key); err != nil {
			return nil, "", err
		}
		if _, err = io.Copy(fw, r); err != nil {
			return nil, "", err
		}
	}
	mw.Close()

	return body, mw.FormDataContentType(), nil
}

type xmlProvider struct {
	payload interface{}
}

func (p xmlProvider) ContentType() string {
	return xmlContentType
}

func (p xmlProvider) Body() (io.Reader, error) {
	values, err := xml.MarshalIndent(&p.payload, " ", "  ")
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(values), nil
}
