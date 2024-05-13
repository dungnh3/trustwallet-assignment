package rest

import (
	"context"
	"encoding/base64"
	goquery "github.com/google/go-querystring/query"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/zap"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

const (
	// plainTextType   = "text/plain; charset=utf-8"
	jsonContentType = "application/json"
	formContentType = "application/x-www-form-urlencoded"
	xmlContentType  = "text/xml" // "application/xml"
)

const (
	// hdrUserAgentKey       = "User-Agent"
	// hdrAcceptKey          = "Accept"
	hdrContentTypeKey = "Content-Type"
	// hdrContentLengthKey   = "Content-Length"
	// hdrContentEncodingKey = "Content-Encoding"
	hdrAuthorizationKey = "Authorization"
)

var (
// jsonCheck = regexp.MustCompile(`(?i:(application|text)/(json|.*\+json|json\-.*)(;|$))`)
// xmlCheck  = regexp.MustCompile(`(?i:(application|text)/(xml|.*\+xml)(;|$))`)

// bufPool = &sync.Pool{New: func() interface{} { return &bytes.Buffer{} }}
)

// Doer executes http requests.  It is implemented by *http.Client.  You can
// wrap *http.Client with layers of Doers to form a stack of client-side
// middleware.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Rest is an HTTP Request builder and sender.
type Rest struct {
	mutex sync.Mutex
	// context
	ctx context.Context
	// http Client for doing requests
	httpClient Doer
	// HTTP method (GET, POST, etc.)
	method string
	// base url string for requests
	baseURL *url.URL
	// raw url string for requests
	rawURL string
	// stores key-values pairs to add to request's Headers
	header http.Header
	// url tagged query structs
	queryStructs []interface{}
	queryParams  map[string]string
	// body provider
	bodyProvider          BodyProvider
	multipartBodyProvider BodyMultipartProvider
	// response decoder
	responseDecoder ResponseDecoder
	// func success decider
	isSuccess SuccessDecider

	counterVec *prometheus.CounterVec
	log        *zap.Logger
}

var defaultClient = &http.Client{ // otelhttp.DefaultClient
	Transport: http.DefaultTransport,
}

// New returns a new Rest with an http defaultClient.
func New(opts ...Option) *Rest {
	c := newConfig()
	for _, opt := range opts {
		opt.apply(c)
	}

	logger, _ := zap.NewProduction()
	return &Rest{
		mutex:           sync.Mutex{},
		httpClient:      c.httpClient,
		method:          http.MethodGet,
		header:          make(http.Header),
		queryStructs:    make([]interface{}, 0),
		queryParams:     make(map[string]string),
		responseDecoder: c.responseDecoder,
		isSuccess:       c.isSuccess,
		log:             logger,
	}
}

func NewOtel(opts ...otelhttp.Option) *Rest {
	napOpt := WithHttpClient(&http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport, opts...),
	})

	return New(napOpt)
}

func (s *Rest) Clone() *Rest {
	// copy Headers pairs into new Header map
	headerCopy := make(http.Header)
	for k, v := range s.header {
		headerCopy[k] = v
	}

	baseURL, _ := url.Parse(s.baseURL.String())
	return &Rest{
		mutex:           sync.Mutex{},
		ctx:             s.ctx,
		httpClient:      s.httpClient,
		method:          s.method,
		baseURL:         baseURL,
		rawURL:          s.rawURL,
		header:          headerCopy,
		queryStructs:    append([]interface{}{}, s.queryStructs...),
		bodyProvider:    s.bodyProvider,
		queryParams:     s.queryParams,
		responseDecoder: s.responseDecoder,
		isSuccess:       s.isSuccess,
		counterVec:      s.counterVec,
		log:             s.log,
	}
}

// Http Client

// Client sets the http Client used to do requests. If a nil client is given,
// the http.defaultClient will be used.
func (s *Rest) Client(httpClient *http.Client) *Rest {
	if httpClient == nil {
		return s.Doer(defaultClient)
	}

	return s.Doer(httpClient)
}

// Doer sets the custom Doer implementation used to do requests.
// If a nil client is given, the http.defaultClient will be used.
func (s *Rest) Doer(doer Doer) *Rest {
	if doer == nil {
		s.httpClient = defaultClient
	} else {
		s.httpClient = doer
	}
	return s
}

// Context method returns the Context if its already set in request
// otherwise it creates new one using `context.Background()`.
func (s *Rest) Context() context.Context {
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}

func (s *Rest) AutoRetry(opts ...RetryOption) *Rest {
	s.httpClient = NewRetryDoer(s.httpClient, s.log, opts...)
	return s
}

// SetContext method sets the context.Context for current Request. It allows
// to interrupt the request execution if ctx.Done() channel is closed.
// See https://blog.golang.org/context article and the "context" package
// documentation.
func (s *Rest) SetContext(ctx context.Context) *Rest {
	s.ctx = ctx
	return s
}

// Debug ...
func (s *Rest) Debug() *Rest {
	return s
}

// CreatePrometheusVec return to register once time: prometheus.MustRegister(counterVec)
func (s *Rest) CreatePrometheusVec(existingVec *prometheus.CounterVec) *prometheus.CounterVec {
	if existingVec != nil {
		s.counterVec = existingVec
		return existingVec
	}

	s.counterVec = NapCounterVec()
	return s.counterVec
}

func NapCounterVec() *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "nap_counter",
	}, []string{"method", "host", "path", "status_code"})
}

// Method

// Head sets the Rest method to HEAD and sets the given pathURL.
func (s *Rest) Head(pathURL string) *Rest {
	s.method = http.MethodHead
	return s.Path(pathURL)
}

// Get sets the Rest method to GET and sets the given pathURL.
func (s *Rest) Get(pathURL string) *Rest {
	s.method = http.MethodGet
	return s.Path(pathURL)
}

// Post sets the Rest method to POST and sets the given pathURL.
func (s *Rest) Post(pathURL string) *Rest {
	s.method = http.MethodPost
	return s.Path(pathURL)
}

// Put sets the Rest method to PUT and sets the given pathURL.
func (s *Rest) Put(pathURL string) *Rest {
	s.method = http.MethodPut
	return s.Path(pathURL)
}

// Patch sets the Rest method to PATCH and sets the given pathURL.
func (s *Rest) Patch(pathURL string) *Rest {
	s.method = http.MethodPatch
	return s.Path(pathURL)
}

// Delete sets the Rest method to DELETE and sets the given pathURL.
func (s *Rest) Delete(pathURL string) *Rest {
	s.method = http.MethodDelete
	return s.Path(pathURL)
}

// Options sets the Rest method to OPTIONS and sets the given pathURL.
func (s *Rest) Options(pathURL string) *Rest {
	s.method = http.MethodOptions
	return s.Path(pathURL)
}

// Header

func (s *Rest) AddHeader(key, value string) *Rest {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.header.Add(key, value)
	return s
}

func (s *Rest) SetHeader(key, value string) *Rest {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.header.Set(key, value)
	return s
}

func (s *Rest) SetHeaders(headers map[string]string) *Rest {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	for h, v := range headers {
		s.header.Set(h, v)
	}
	return s
}

func (s *Rest) SetBasicAuth(username, password string) *Rest {
	return s.SetHeader(hdrAuthorizationKey, "Basic "+base64.StdEncoding.EncodeToString([]byte(username+":"+password)))
}

func (s *Rest) SetAuthToken(token string) *Rest {
	return s.SetHeader(hdrAuthorizationKey, "Bearer "+token)
}

func (s *Rest) WithSuccessDecider(isSuccess SuccessDecider) *Rest {
	s.isSuccess = isSuccess
	return s
}

// Url

// Base sets the baseURL. If you intend to extend the url with Path,
// baseUrl should be specified with a trailing slash.
func (s *Rest) Base(baseURL string) *Rest {
	var err error
	s.baseURL, err = url.Parse(baseURL)
	if err != nil {
		panic(err)
	}

	s.rawURL = s.baseURL.String()
	return s
}

// Path extends the rawURL with the given path by resolving the reference to
// an absolute URL. If parsing errors occur, the rawURL is left unmodified.
func (s *Rest) Path(path string) *Rest {
	var err error
	pathURL := &url.URL{}
	if s.baseURL == nil {
		s.baseURL, err = url.Parse(path)
		if err != nil {
			return s
		}

		pathURL = s.baseURL
	} else {
		pathURL, err = url.Parse(path)
		if err != nil {
			return s
		}
	}

	s.rawURL = s.baseURL.ResolveReference(pathURL).String()
	if strings.HasSuffix(path, "/") && !strings.HasSuffix(s.rawURL, "/") {
		s.rawURL += "/"
	}
	return s
}

// QueryStruct appends the queryStruct to the Rest's queryStructs. The value
// pointed to by each queryStruct will be encoded as url query parameters on
// new requests (see Request()).
// The queryStruct argument should be a pointer to a url tagged struct. See
// https://godoc.org/github.com/google/go-querystring/query for details.
func (s *Rest) QueryStruct(queryStruct interface{}) *Rest {
	if queryStruct != nil {
		s.queryStructs = append(s.queryStructs, queryStruct)
	}
	s.log.Info("QueryStruct", zap.String(s.method, s.rawURL), zap.Any("body", s.queryStructs))
	return s
}

func (s *Rest) QueryParams(params map[string]string) *Rest {
	if params != nil {
		s.queryParams = params
	}
	s.log.Info("QueryParams", zap.String(s.method, s.rawURL), zap.Any("body", s.queryParams))
	return s
}

// Body

// Body sets the Rest's body. The body value will be set as the Body on new
// requests (see Request()).
// If the provided body is also an io.Closer, the request Body will be closed
// by http.Client methods.
func (s *Rest) Body(body io.Reader) *Rest {
	if body == nil {
		return s
	}
	return s.BodyProvider(bodyProvider{body: body})
}

// BodyProvider sets the Rest's body provider.
func (s *Rest) BodyProvider(body BodyProvider) *Rest {
	if body == nil {
		return s
	}

	s.bodyProvider = body
	s.multipartBodyProvider = nil

	ct := body.ContentType()
	if ct != "" {
		s.SetHeader(hdrContentTypeKey, ct)
	}

	return s
}

// BodyMultipartProvider ...
func (s *Rest) BodyMultipartProvider(body BodyMultipartProvider) *Rest {
	if body == nil {
		return s
	}

	s.bodyProvider = nil
	s.multipartBodyProvider = body

	return s
}

// BodyJSON sets the Rest's bodyJSON. The value pointed to by the bodyJSON
// will be JSON encoded as the Body on new requests (see Request()).
// The bodyJSON argument should be a pointer to a JSON tagged struct. See
// https://golang.org/pkg/encoding/json/#MarshalIndent for details.
func (s *Rest) BodyJSON(bodyJSON interface{}) *Rest {
	if bodyJSON == nil {
		return s
	}
	return s.BodyProvider(jsonBodyProvider{payload: bodyJSON})
}

// BodyForm sets the Rest's bodyForm. The value pointed to by the bodyForm
// will be url encoded as the Body on new requests (see Request()).
// The bodyForm argument should be a pointer to a url tagged struct. See
// https://godoc.org/github.com/google/go-querystring/query for details.
func (s *Rest) BodyForm(bodyForm interface{}) *Rest {
	if bodyForm == nil {
		return s
	}
	return s.BodyProvider(formBodyProvider{payload: bodyForm})
}

// BodyUrlEncode ...
func (s *Rest) BodyUrlEncode(values map[string]string) *Rest {
	if values == nil {
		return s
	}
	return s.BodyProvider(formUrlEncodedProvider{values: values})
}

// BodyMultipart ...
func (s *Rest) BodyMultipart(payload, filePayload map[string]io.Reader) *Rest {
	if payload == nil && filePayload == nil {
		return s
	}
	return s.BodyMultipartProvider(multipartDataBodyProvider{payload: payload, filePayload: filePayload})
}

// BodyXML ...
func (s *Rest) BodyXML(bodyXml interface{}) *Rest {
	if bodyXml == nil {
		return s
	}
	return s.BodyProvider(xmlProvider{payload: bodyXml})
}

// Requests

// Request returns a new http.Request created with the Rest properties.
// Returns any errors parsing the rawURL, encoding query structs, encoding
// the body, or creating the http.Request.
func (s *Rest) Request() (*http.Request, error) {
	reqURL, err := url.Parse(s.rawURL)
	if err != nil {
		return nil, err
	}

	err = buildQueryParamUrl(reqURL, s.queryStructs, s.queryParams)
	if err != nil {
		return nil, err
	}

	var body io.Reader
	if s.multipartBodyProvider != nil {
		var ct string
		body, ct, err = s.multipartBodyProvider.Body()
		if err != nil {
			return nil, err
		}

		s.SetHeader(hdrContentTypeKey, ct)
	} else if s.bodyProvider != nil {
		body, err = s.bodyProvider.Body()
		if err != nil {
			return nil, err
		}
	}

	req, err := http.NewRequestWithContext(s.Context(), s.method, reqURL.String(), body)
	if err != nil {
		return nil, err
	}
	addHeaders(req, s.header)
	return req, err
}

// buildQueryParamUrl parses url tagged query structs using go-querystring to
// encode them to url.Values and format them onto the url.RawQuery. Any
// query parsing or encoding errors are returned.
func buildQueryParamUrl(reqURL *url.URL, queryStructs []interface{}, queryParams map[string]string) error {
	urlValues, err := url.ParseQuery(reqURL.RawQuery)
	if err != nil {
		return err
	}
	// encodes query structs into a url.Values map and merges maps
	for _, queryStruct := range queryStructs {
		queryValues, err := goquery.Values(queryStruct)
		if err != nil {
			return err
		}
		for key, values := range queryValues {
			for _, value := range values {
				urlValues.Add(key, value)
			}
		}
	}
	for k, v := range queryParams {
		urlValues.Add(k, v)
	}
	// url.Values format to a sorted "url encoded" string, e.g. "key=val&foo=bar"
	reqURL.RawQuery = urlValues.Encode()
	return nil
}

// addHeaders adds the key, value pairs from the given http.Header to the
// request. Values for existing keys are appended to the keys values.
func addHeaders(req *http.Request, header http.Header) {
	for key, values := range header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
}

// Sending

// ResponseDecoder sets the Rest's response decoder.
func (s *Rest) ResponseDecoder(decoder ResponseDecoder) *Rest {
	if decoder == nil {
		return s
	}
	s.responseDecoder = decoder
	return s
}

// ReceiveSuccess creates a new HTTP request and returns the response. Success
// responses (2XX) are JSON decoded into the value pointed to by successV.
// Any error creating the request, sending it, or decoding a 2XX response
// is returned.
func (s *Rest) ReceiveSuccess(successV interface{}) (*Response, error) {
	return s.Receive(successV, nil)
}

// Receive creates a new HTTP request and returns the response. Success
// responses (2XX) are JSON decoded into the value pointed to by successV and
// other responses are JSON decoded into the value pointed to by failureV.
// If the status code of response is 204(no content), decoding is skipped.
// Any error creating the request, sending it, or decoding the response is
// returned.
// Receive is shorthand for calling Request and Do.
func (s *Rest) Receive(successV, failureV interface{}) (*Response, error) {
	req, err := s.Request()
	if err != nil {
		return nil, err
	}
	return s.Do(req, successV, failureV)
}

// Do send an HTTP request and returns the response. Success responses (2XX)
// are JSON decoded into the value pointed to by successV and other responses
// are JSON decoded into the value pointed to by failureV.
// If the status code of response is 204(no content), decoding is skipped.
// Any error sending the request or decoding the response is returned.
func (s *Rest) Do(req *http.Request, successV, failureV interface{}) (*Response, error) {
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return NewResponse(resp), err
	}
	// when err is nil, resp contains a non-nil resp.Body which must be closed
	defer resp.Body.Close()

	// The default HTTP client's Transport may not
	// reuse HTTP/1.x "keep-alive" TCP connections if the Body is
	// not read to completion and closed.
	// See: https://golang.org/pkg/net/http/#Response
	//nolint:errcheck
	defer io.Copy(ioutil.Discard, resp.Body)

	// Don't try to decode on 204s
	if resp.StatusCode == http.StatusNoContent {
		return NewResponse(resp), nil
	}

	// Decode from json
	if successV != nil || failureV != nil {
		err = s.decodeResponse(resp, successV, failureV)
	}
	return NewResponse(resp), err
}

// decodeResponse decodes response Body into the value pointed to by successV
// if the response is a success (2XX) or into the value pointed to by failureV
// otherwise. If the successV or failureV argument to decode into is nil,
// decoding is skipped.
// Caller is responsible for closing the resp.Body.
func (s *Rest) decodeResponse(resp *http.Response, successV, failureV interface{}) error {
	if s.counterVec != nil {
		s.counterVec.WithLabelValues(s.method, s.baseURL.Host, s.rawURL, strconv.Itoa(resp.StatusCode)).Add(1)
	}

	if s.isSuccess(resp) {
		switch sv := successV.(type) {
		case nil:
			return nil
		case *Raw:
			respBody, err := ioutil.ReadAll(resp.Body)
			*sv = respBody
			s.log.Info("decode success-raw", zap.String(s.method, s.rawURL), zap.Any("resp", respBody), zap.Error(err))
			return err
		default:
			err := s.responseDecoder.Decode(resp, successV)
			s.log.Info("decode success-resp", zap.String(s.method, s.rawURL), zap.Any("resp", successV), zap.Error(err))
			return err
		}
	} else {
		switch fv := failureV.(type) {
		case nil:
			respBody, err := ioutil.ReadAll(resp.Body)
			s.log.Warn("decode failure-nil", zap.String(s.method, s.rawURL), zap.String("status", resp.Status), zap.Any("resp", respBody), zap.Error(err))
			return nil
		case *Raw:
			respBody, err := ioutil.ReadAll(resp.Body)
			*fv = respBody
			s.log.Warn("decode failure-raw", zap.String(s.method, s.rawURL), zap.String("status", resp.Status), zap.Any("resp", respBody), zap.Error(err))
			return err
		default:
			err := s.responseDecoder.Decode(resp, failureV)
			s.log.Warn("decode failure-resp", zap.String(s.method, s.rawURL), zap.String("status", resp.Status), zap.Any("resp", failureV), zap.Error(err))
			return err
		}
	}
}
