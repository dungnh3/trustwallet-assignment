package rest

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

type FakeParams struct {
	KindName string `url:"kind_name"`
	Count    int    `url:"count"`
}

// Url-tagged query struct
var paramsA = struct {
	Limit int `url:"limit"`
}{
	30,
}
var paramsB = FakeParams{KindName: "recent", Count: 25}

// Json/XML-tagged model struct
type FakeModel struct {
	Text          string  `json:"text,omitempty" xml:"text"`
	FavoriteCount int64   `json:"favorite_count,omitempty" xml:"favorite_count"`
	Temperature   float64 `json:"temperature,omitempty" xml:"temperature"`
}

var modelA = FakeModel{Text: "note", FavoriteCount: 12}

// Non-Json response decoder
type xmlResponseDecoder struct{}

func (d xmlResponseDecoder) Decode(resp *http.Response, v interface{}) error {
	return xml.NewDecoder(resp.Body).Decode(v)
}

func TestNew(t *testing.T) {
	nap := New()
	if nap.httpClient != defaultClient {
		t.Errorf("expected %v, got %v", defaultClient, nap.httpClient)
	}
	if nap.header == nil {
		t.Errorf("Header map not initialized with make")
	}
	if nap.queryStructs == nil {
		t.Errorf("queryStructs not initialized with make")
	}
}

func TestNapNew(t *testing.T) {
	fakeBodyProvider := jsonBodyProvider{FakeModel{}}

	cases := []*Rest{
		&Rest{httpClient: &http.Client{}, method: "GET", rawURL: "https://example.com"},
		&Rest{httpClient: nil, method: "", rawURL: "https://example.com"},
		&Rest{queryStructs: make([]interface{}, 0)},
		&Rest{queryStructs: []interface{}{paramsA}},
		&Rest{queryStructs: []interface{}{paramsA, paramsB}},
		&Rest{bodyProvider: fakeBodyProvider},
		&Rest{bodyProvider: fakeBodyProvider},
		&Rest{bodyProvider: nil},
		New().AddHeader("Content-Type", "application/json"),
		New().AddHeader("A", "B").AddHeader("a", "c").Clone(),
		New().AddHeader("A", "B").Clone().AddHeader("a", "c"),
		New().BodyForm(paramsB),
		New().BodyForm(paramsB).Clone(),
	}
	for _, nap := range cases {
		child := nap.Clone()
		if child.httpClient != nap.httpClient {
			t.Errorf("expected %v, got %v", nap.httpClient, child.httpClient)
		}
		if child.method != nap.method {
			t.Errorf("expected %s, got %s", nap.method, child.method)
		}
		if child.rawURL != nap.rawURL {
			t.Errorf("expected %s, got %s", nap.rawURL, child.rawURL)
		}
		// Header should be a copy of parent Rest header. For example, calling
		// baseNap.Add("k","v") should not mutate previously created child Naps
		if nap.header != nil {
			// struct literal cases don't init Header in usual way, skip header check
			if !reflect.DeepEqual(nap.header, child.header) {
				t.Errorf("not DeepEqual: expected %v, got %v", nap.header, child.header)
			}
			nap.header.Add("K", "V")
			if child.header.Get("K") != "" {
				t.Errorf("child.header was a reference to original map, should be copy")
			}
		}
		// queryStruct slice should be a new slice with a copy of the contents
		if len(nap.queryStructs) > 0 {
			// mutating one slice should not mutate the other
			child.queryStructs[0] = nil
			if nap.queryStructs[0] == nil {
				t.Errorf("child.queryStructs was a re-slice, expected slice with copied contents")
			}
		}
		// body should be copied
		if child.bodyProvider != nap.bodyProvider {
			t.Errorf("expected %v, got %v", nap.bodyProvider, child.bodyProvider)
		}
	}
}

func TestClientSetter(t *testing.T) {
	developerClient := &http.Client{}
	cases := []struct {
		input    *http.Client
		expected *http.Client
	}{
		{nil, defaultClient},
		{developerClient, developerClient},
	}
	for _, c := range cases {
		nap := New()
		nap.Client(c.input)
		if nap.httpClient != c.expected {
			t.Errorf("input %v, expected %v, got %v", c.input, c.expected, nap.httpClient)
		}
	}
}

func TestDoerSetter(t *testing.T) {
	developerClient := &http.Client{}
	cases := []struct {
		input    Doer
		expected Doer
	}{
		{nil, defaultClient},
		{developerClient, developerClient},
	}
	for _, c := range cases {
		nap := New()
		nap.Doer(c.input)
		if nap.httpClient != c.expected {
			t.Errorf("input %v, expected %v, got %v", c.input, c.expected, nap.httpClient)
		}
	}
}

func TestBaseSetter(t *testing.T) {
	cases := []string{"https://a.io/", "https://b.io", "/path", "path", ""}
	for _, base := range cases {
		nap := New().Base(base)
		if nap.rawURL != base {
			t.Errorf("expected %s, got %s", base, nap.rawURL)
		}
	}
}

func TestPathSetter(t *testing.T) {
	cases := []struct {
		rawURL         string
		path           string
		expectedRawURL string
	}{
		{"https://a.io/", "foo", "https://a.io/foo"},
		{"https://a.io/", "/foo", "https://a.io/foo"},
		{"https://a.io", "foo", "https://a.io/foo"},
		{"https://a.io", "/foo", "https://a.io/foo"},
		{"https://a.io/foo/", "bar", "https://a.io/foo/bar"},
		// rawURL should end in trailing slash if it is to be Path extended
		{"https://a.io/foo", "bar", "https://a.io/bar"},
		{"https://a.io/foo", "/bar", "https://a.io/bar"},
		// path extension is absolute
		{"https://a.io", "https://b.io/", "https://b.io/"},
		{"https://a.io/", "https://b.io/", "https://b.io/"},
		{"https://a.io", "https://b.io", "https://b.io"},
		{"https://a.io/", "https://b.io", "https://b.io"},
		// empty base, empty path
		{"", "https://b.io", "https://b.io"},
		{"https://a.io", "", "https://a.io"},
		{"", "", ""},
	}
	for _, c := range cases {
		nap := New().Base(c.rawURL).Path(c.path)
		if nap.rawURL != c.expectedRawURL {
			t.Errorf("expected %s, got %s", c.expectedRawURL, nap.rawURL)
		}
	}
}

func TestMethodSetters(t *testing.T) {
	cases := []struct {
		nap            *Rest
		expectedMethod string
	}{
		{New().Path("https://a.io"), "GET"},
		{New().Head("https://a.io"), "HEAD"},
		{New().Get("https://a.io"), "GET"},
		{New().Post("https://a.io"), "POST"},
		{New().Put("https://a.io"), "PUT"},
		{New().Patch("https://a.io"), "PATCH"},
		{New().Delete("https://a.io"), "DELETE"},
		{New().Options("https://a.io"), "OPTIONS"},
	}
	for _, c := range cases {
		if c.nap.method != c.expectedMethod {
			t.Errorf("expected method %s, got %s", c.expectedMethod, c.nap.method)
		}
	}
}

func TestAddHeader(t *testing.T) {
	cases := []struct {
		nap            *Rest
		expectedHeader map[string][]string
	}{
		{New().AddHeader("authorization", "OAuth key=\"value\""), map[string][]string{"Authorization": []string{"OAuth key=\"value\""}}},
		// header keys should be canonicalized
		{New().AddHeader("content-tYPE", "application/json").AddHeader("User-AGENT", "nap"), map[string][]string{"Content-Type": []string{"application/json"}, "User-Agent": []string{"nap"}}},
		// values for existing keys should be appended
		{New().AddHeader("A", "B").AddHeader("a", "c"), map[string][]string{"A": []string{"B", "c"}}},
		// Add should add to values for keys added by parent Naps
		{New().AddHeader("A", "B").AddHeader("a", "c").Clone(), map[string][]string{"A": []string{"B", "c"}}},
		{New().AddHeader("A", "B").Clone().AddHeader("a", "c"), map[string][]string{"A": []string{"B", "c"}}},
	}
	for _, c := range cases {
		// type conversion from header to alias'd map for deep equality comparison
		headerMap := map[string][]string(c.nap.header)
		if !reflect.DeepEqual(c.expectedHeader, headerMap) {
			t.Errorf("not DeepEqual: expected %v, got %v", c.expectedHeader, headerMap)
		}
	}
}

func TestSetHeader(t *testing.T) {
	cases := []struct {
		nap            *Rest
		expectedHeader map[string][]string
	}{
		// should replace existing values associated with key
		{New().AddHeader("A", "B").SetHeader("a", "c"), map[string][]string{"A": []string{"c"}}},
		{New().SetHeader("content-type", "A").SetHeader("Content-Type", "B"), map[string][]string{"Content-Type": []string{"B"}}},
		// Set should replace values received by copying parent Naps
		{New().SetHeader("A", "B").AddHeader("a", "c").Clone(), map[string][]string{"A": []string{"B", "c"}}},
		{New().AddHeader("A", "B").Clone().SetHeader("a", "c"), map[string][]string{"A": []string{"c"}}},
	}
	for _, c := range cases {
		// type conversion from Header to alias'd map for deep equality comparison
		headerMap := map[string][]string(c.nap.header)
		if !reflect.DeepEqual(c.expectedHeader, headerMap) {
			t.Errorf("not DeepEqual: expected %v, got %v", c.expectedHeader, headerMap)
		}
	}
}

func TestBasicAuth(t *testing.T) {
	cases := []struct {
		nap          *Rest
		expectedAuth []string
	}{
		// basic auth: username & password
		{New().SetBasicAuth("Aladdin", "open sesame"), []string{"Aladdin", "open sesame"}},
		// empty username
		{New().SetBasicAuth("", "secret"), []string{"", "secret"}},
		// empty password
		{New().SetBasicAuth("admin", ""), []string{"admin", ""}},
	}
	for _, c := range cases {
		req, err := c.nap.Request()
		if err != nil {
			t.Errorf("unexpected error when building Request with .SetBasicAuth()")
		}
		username, password, ok := req.BasicAuth()
		if !ok {
			t.Errorf("basic auth missing when expected")
		}
		auth := []string{username, password}
		if !reflect.DeepEqual(c.expectedAuth, auth) {
			t.Errorf("not DeepEqual: expected %v, got %v", c.expectedAuth, auth)
		}
	}
}

func TestQueryStructSetter(t *testing.T) {
	cases := []struct {
		nap             *Rest
		expectedStructs []interface{}
	}{
		{New(), []interface{}{}},
		{New().QueryStruct(nil), []interface{}{}},
		{New().QueryStruct(paramsA), []interface{}{paramsA}},
		{New().QueryStruct(paramsA).QueryStruct(paramsA), []interface{}{paramsA, paramsA}},
		{New().QueryStruct(paramsA).QueryStruct(paramsB), []interface{}{paramsA, paramsB}},
		{New().QueryStruct(paramsA).Clone(), []interface{}{paramsA}},
		{New().QueryStruct(paramsA).Clone().QueryStruct(paramsB), []interface{}{paramsA, paramsB}},
	}

	for _, c := range cases {
		if count := len(c.nap.queryStructs); count != len(c.expectedStructs) {
			t.Errorf("expected length %d, got %d", len(c.expectedStructs), count)
		}
	check:
		for _, expected := range c.expectedStructs {
			for _, param := range c.nap.queryStructs {
				if param == expected {
					continue check
				}
			}
			t.Errorf("expected to find %v in %v", expected, c.nap.queryStructs)
		}
	}
}

func TestBodyJSONSetter(t *testing.T) {
	fakeModel := &FakeModel{}
	fakeBodyProvider := jsonBodyProvider{payload: fakeModel}

	cases := []struct {
		initial  BodyProvider
		input    interface{}
		expected BodyProvider
	}{
		// json tagged struct is set as bodyJSON
		{nil, fakeModel, fakeBodyProvider},
		// nil argument to bodyJSON does not replace existing bodyJSON
		{fakeBodyProvider, nil, fakeBodyProvider},
		// nil bodyJSON remains nil
		{nil, nil, nil},
	}
	for _, c := range cases {
		nap := New()
		nap.bodyProvider = c.initial
		nap.BodyJSON(c.input)
		if nap.bodyProvider != c.expected {
			t.Errorf("expected %v, got %v", c.expected, nap.bodyProvider)
		}
		// Header Content-Type should be application/json if bodyJSON arg was non-nil
		if c.input != nil && nap.header.Get(hdrContentTypeKey) != jsonContentType {
			t.Errorf("Incorrect or missing header, expected %s, got %s", jsonContentType, nap.header.Get(hdrContentTypeKey))
		} else if c.input == nil && nap.header.Get(hdrContentTypeKey) != "" {
			t.Errorf("did not expect a Content-Type header, got %s", nap.header.Get(hdrContentTypeKey))
		}
	}
}

func TestBodyFormSetter(t *testing.T) {
	fakeParams := FakeParams{KindName: "recent", Count: 25}
	fakeBodyProvider := formBodyProvider{payload: fakeParams}

	cases := []struct {
		initial  BodyProvider
		input    interface{}
		expected BodyProvider
	}{
		// url tagged struct is set as bodyStruct
		{nil, paramsB, fakeBodyProvider},
		// nil argument to bodyStruct does not replace existing bodyStruct
		{fakeBodyProvider, nil, fakeBodyProvider},
		// nil bodyStruct remains nil
		{nil, nil, nil},
	}
	for _, c := range cases {
		nap := New()
		nap.bodyProvider = c.initial
		nap.BodyForm(c.input)
		if nap.bodyProvider != c.expected {
			t.Errorf("expected %v, got %v", c.expected, nap.bodyProvider)
		}
		// Content-Type should be application/x-www-form-urlencoded if bodyStruct was non-nil
		if c.input != nil && nap.header.Get(hdrContentTypeKey) != formContentType {
			t.Errorf("Incorrect or missing header, expected %s, got %s", formContentType, nap.header.Get(hdrContentTypeKey))
		} else if c.input == nil && nap.header.Get(hdrContentTypeKey) != "" {
			t.Errorf("did not expect a Content-Type header, got %s", nap.header.Get(hdrContentTypeKey))
		}
	}
}

func TestBodySetter(t *testing.T) {
	fakeInput := ioutil.NopCloser(strings.NewReader("test"))
	fakeBodyProvider := bodyProvider{body: fakeInput}

	cases := []struct {
		initial  BodyProvider
		input    io.Reader
		expected BodyProvider
	}{
		// nil body is overriden by a set body
		{nil, fakeInput, fakeBodyProvider},
		// initial body is not overriden by nil body
		{fakeBodyProvider, nil, fakeBodyProvider},
		// nil body is returned unaltered
		{nil, nil, nil},
	}
	for _, c := range cases {
		nap := New()
		nap.bodyProvider = c.initial
		nap.Body(c.input)
		if nap.bodyProvider != c.expected {
			t.Errorf("expected %v, got %v", c.expected, nap.bodyProvider)
		}
	}
}

func TestRequest_urlAndMethod(t *testing.T) {
	cases := []struct {
		nap            *Rest
		expectedMethod string
		expectedURL    string
		expectedErr    error
	}{
		{New().Base("https://a.io"), "GET", "https://a.io", nil},
		{New().Path("https://a.io"), "GET", "https://a.io", nil},
		{New().Get("https://a.io"), "GET", "https://a.io", nil},
		{New().Put("https://a.io"), "PUT", "https://a.io", nil},
		{New().Base("https://a.io/").Path("foo"), "GET", "https://a.io/foo", nil},
		{New().Base("https://a.io/").Post("foo"), "POST", "https://a.io/foo", nil},
		// if relative path is an absolute url, base is ignored
		{New().Base("https://a.io").Path("https://b.io"), "GET", "https://b.io", nil},
		{New().Path("https://a.io").Path("https://b.io"), "GET", "https://b.io", nil},
		// last method setter takes priority
		{New().Get("https://b.io").Post("https://a.io"), "POST", "https://a.io", nil},
		{New().Post("https://a.io/").Put("foo/").Delete("bar"), "DELETE", "https://a.io/foo/bar", nil},
		// last Base setter takes priority
		{New().Base("https://a.io").Base("https://b.io"), "GET", "https://b.io", nil},
		// Path setters are additive
		{New().Base("https://a.io/").Path("foo/").Path("bar"), "GET", "https://a.io/foo/bar", nil},
		{New().Path("https://a.io/").Path("foo/").Path("bar"), "GET", "https://a.io/foo/bar", nil},
		// removes extra '/' between base and ref url
		{New().Base("https://a.io/").Get("/foo"), "GET", "https://a.io/foo", nil},
	}
	for _, c := range cases {
		req, err := c.nap.Request()
		if err != c.expectedErr {
			t.Errorf("expected error %v, got %v for %+v", c.expectedErr, err, c.nap)
		}
		if req.URL.String() != c.expectedURL {
			t.Errorf("expected url %s, got %s for %+v", c.expectedURL, req.URL.String(), c.nap)
		}
		if req.Method != c.expectedMethod {
			t.Errorf("expected method %s, got %s for %+v", c.expectedMethod, req.Method, c.nap)
		}
	}
}

func TestRequest_queryStructs(t *testing.T) {
	cases := []struct {
		nap         *Rest
		expectedURL string
	}{
		{New().Base("https://a.io").QueryStruct(paramsA), "https://a.io?limit=30"},
		{New().Base("https://a.io").QueryStruct(paramsA).QueryStruct(paramsB), "https://a.io?count=25&kind_name=recent&limit=30"},
		{New().Base("https://a.io/").Path("foo?path=yes").QueryStruct(paramsA), "https://a.io/foo?limit=30&path=yes"},
		{New().Base("https://a.io").QueryStruct(paramsA).Clone(), "https://a.io?limit=30"},
		{New().Base("https://a.io").QueryStruct(paramsA).Clone().QueryStruct(paramsB), "https://a.io?count=25&kind_name=recent&limit=30"},
	}
	for _, c := range cases {
		req, _ := c.nap.Request()
		if req.URL.String() != c.expectedURL {
			t.Errorf("expected url %s, got %s for %+v", c.expectedURL, req.URL.String(), c.nap)
		}
	}
}

func TestRequest_body(t *testing.T) {
	cases := []struct {
		nap                 *Rest
		expectedBody        string // expected Body io.Reader as a string
		expectedContentType string
	}{
		// BodyJSON
		{New().BodyJSON(modelA), "{\"text\":\"note\",\"favorite_count\":12}\n", jsonContentType},
		{New().BodyJSON(&modelA), "{\"text\":\"note\",\"favorite_count\":12}\n", jsonContentType},
		{New().BodyJSON(&FakeModel{}), "{}\n", jsonContentType},
		{New().BodyJSON(FakeModel{}), "{}\n", jsonContentType},
		// BodyJSON overrides existing values
		{New().BodyJSON(&FakeModel{}).BodyJSON(&FakeModel{Text: "msg"}), "{\"text\":\"msg\"}\n", jsonContentType},
		// BodyForm
		{New().BodyForm(paramsA), "limit=30", formContentType},
		{New().BodyForm(paramsB), "count=25&kind_name=recent", formContentType},
		{New().BodyForm(&paramsB), "count=25&kind_name=recent", formContentType},
		// BodyForm overrides existing values
		{New().BodyForm(paramsA).Clone().BodyForm(paramsB), "count=25&kind_name=recent", formContentType},
		// Mixture of BodyJSON and BodyForm prefers body setter called last with a non-nil argument
		{New().BodyForm(paramsB).Clone().BodyJSON(modelA), "{\"text\":\"note\",\"favorite_count\":12}\n", jsonContentType},
		{New().BodyJSON(modelA).Clone().BodyForm(paramsB), "count=25&kind_name=recent", formContentType},
		{New().BodyForm(paramsB).Clone().BodyJSON(nil), "count=25&kind_name=recent", formContentType},
		{New().BodyJSON(modelA).Clone().BodyForm(nil), "{\"text\":\"note\",\"favorite_count\":12}\n", jsonContentType},
		// Body
		{New().Body(strings.NewReader("this-is-a-test")), "this-is-a-test", ""},
		{New().Body(strings.NewReader("a")).Body(strings.NewReader("b")), "b", ""},
	}
	for _, c := range cases {
		req, _ := c.nap.Request()
		buf := new(bytes.Buffer)
		buf.ReadFrom(req.Body)
		// req.Body should have contained the expectedBody string
		if value := buf.String(); value != c.expectedBody {
			t.Errorf("expected Request.Body %s, got %s", c.expectedBody, value)
		}
		// Header Content-Type should be expectedContentType ("" means no hdrContentTypeKey expected)
		if actualHeader := req.Header.Get(hdrContentTypeKey); actualHeader != c.expectedContentType && c.expectedContentType != "" {
			t.Errorf("Incorrect or missing header, expected %s, got %s", c.expectedContentType, actualHeader)
		}
	}
}

func TestRequest_bodyNoData(t *testing.T) {
	// test that Body is left nil when no bodyJSON or bodyStruct set
	naps := []*Rest{
		New(),
		New().BodyJSON(nil),
		New().BodyForm(nil),
	}
	for _, nap := range naps {
		req, _ := nap.Request()
		if req.Body != nil {
			t.Errorf("expected nil Request.Body, got %v", req.Body)
		}
		// Header Content-Type should not be set when bodyJSON argument was nil or never called
		if actualHeader := req.Header.Get(hdrContentTypeKey); actualHeader != "" {
			t.Errorf("did not expect a Content-Type header, got %s", actualHeader)
		}
	}
}

func TestRequest_bodyEncodeErrors(t *testing.T) {
	cases := []struct {
		nap         *Rest
		expectedErr error
	}{
		// check that Encode errors are propagated, illegal JSON field
		{New().BodyJSON(FakeModel{Temperature: math.Inf(1)}), errors.New("json: unsupported value: +Inf")},
	}
	for _, c := range cases {
		req, err := c.nap.Request()
		if err == nil || err.Error() != c.expectedErr.Error() {
			t.Errorf("expected error %v, got %v", c.expectedErr, err)
		}
		if req != nil {
			t.Errorf("expected nil Request, got %+v", req)
		}
	}
}

func TestRequest_headers(t *testing.T) {
	cases := []struct {
		nap            *Rest
		expectedHeader map[string][]string
	}{
		{New().AddHeader("authorization", "OAuth key=\"value\""), map[string][]string{"Authorization": []string{"OAuth key=\"value\""}}},
		// header keys should be canonicalized
		{New().AddHeader("content-tYPE", "application/json").AddHeader("User-AGENT", "nap"), map[string][]string{"Content-Type": []string{"application/json"}, "User-Agent": []string{"nap"}}},
		// values for existing keys should be appended
		{New().AddHeader("A", "B").AddHeader("a", "c"), map[string][]string{"A": []string{"B", "c"}}},
		// Add should add to values for keys added by parent Naps
		{New().AddHeader("A", "B").AddHeader("a", "c").Clone(), map[string][]string{"A": []string{"B", "c"}}},
		{New().AddHeader("A", "B").Clone().AddHeader("a", "c"), map[string][]string{"A": []string{"B", "c"}}},
		// Add and Set
		{New().AddHeader("A", "B").SetHeader("a", "c"), map[string][]string{"A": []string{"c"}}},
		{New().SetHeader("content-type", "A").SetHeader("Content-Type", "B"), map[string][]string{"Content-Type": []string{"B"}}},
		// Set should replace values received by copying parent Naps
		{New().SetHeader("A", "B").AddHeader("a", "c").Clone(), map[string][]string{"A": []string{"B", "c"}}},
		{New().AddHeader("A", "B").Clone().SetHeader("a", "c"), map[string][]string{"A": []string{"c"}}},
	}
	for _, c := range cases {
		req, _ := c.nap.Request()
		// type conversion from Header to alias'd map for deep equality comparison
		headerMap := map[string][]string(req.Header)
		if !reflect.DeepEqual(c.expectedHeader, headerMap) {
			t.Errorf("not DeepEqual: expected %v, got %v", c.expectedHeader, headerMap)
		}
	}
}

func TestAddQueryStructs(t *testing.T) {
	cases := []struct {
		rawurl       string
		queryStructs []interface{}
		expected     string
	}{
		{"https://a.io", []interface{}{}, "https://a.io"},
		{"https://a.io", []interface{}{paramsA}, "https://a.io?limit=30"},
		{"https://a.io", []interface{}{paramsA, paramsA}, "https://a.io?limit=30&limit=30"},
		{"https://a.io", []interface{}{paramsA, paramsB}, "https://a.io?count=25&kind_name=recent&limit=30"},
		// don't blow away query values on the rawURL (parsed into RawQuery)
		{"https://a.io?initial=7", []interface{}{paramsA}, "https://a.io?initial=7&limit=30"},
	}
	for _, c := range cases {
		reqURL, _ := url.Parse(c.rawurl)
		buildQueryParamUrl(reqURL, c.queryStructs, map[string]string{})
		if reqURL.String() != c.expected {
			t.Errorf("expected %s, got %s", c.expected, reqURL.String())
		}
	}
}

// Sending

type APIError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

func TestDo_onSuccess(t *testing.T) {
	const expectedText = "Some text"
	const expectedFavoriteCount int64 = 24

	client, mux, server := testServer()
	defer server.Close()
	mux.HandleFunc("/success", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"text": "Some text", "favorite_count": 24}`)
	})

	nap := New().Client(client)
	req, _ := http.NewRequest("GET", "https://example.com/success", nil)

	model := new(FakeModel)
	apiError := new(APIError)
	resp, err := nap.Do(req, model, apiError)

	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected %d, got %d", 200, resp.StatusCode)
	}
	if model.Text != expectedText {
		t.Errorf("expected %s, got %s", expectedText, model.Text)
	}
	if model.FavoriteCount != expectedFavoriteCount {
		t.Errorf("expected %d, got %d", expectedFavoriteCount, model.FavoriteCount)
	}
}

func TestDo_onSuccessWithNilValue(t *testing.T) {
	client, mux, server := testServer()
	defer server.Close()
	mux.HandleFunc("/success", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"text": "Some text", "favorite_count": 24}`)
	})

	nap := New().Client(client)
	req, _ := http.NewRequest("GET", "https://example.com/success", nil)

	apiError := new(APIError)
	resp, err := nap.Do(req, nil, apiError)

	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected %d, got %d", 200, resp.StatusCode)
	}
	expected := &APIError{}
	if !reflect.DeepEqual(expected, apiError) {
		t.Errorf("failureV should not be populated, exepcted %v, got %v", expected, apiError)
	}
}

func TestDo_noContent(t *testing.T) {
	client, mux, server := testServer()
	defer server.Close()
	mux.HandleFunc("/nocontent", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})

	nap := New().Client(client)
	req, _ := http.NewRequest("DELETE", "https://example.com/nocontent", nil)

	model := new(FakeModel)
	apiError := new(APIError)
	resp, err := nap.Do(req, model, apiError)

	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if resp.StatusCode != 204 {
		t.Errorf("expected %d, got %d", 204, resp.StatusCode)
	}
	expectedModel := &FakeModel{}
	if !reflect.DeepEqual(expectedModel, model) {
		t.Errorf("successV should not be populated, exepcted %v, got %v", expectedModel, model)
	}
	expectedAPIError := &APIError{}
	if !reflect.DeepEqual(expectedAPIError, apiError) {
		t.Errorf("failureV should not be populated, exepcted %v, got %v", expectedAPIError, apiError)
	}
}

func TestDo_onFailure(t *testing.T) {
	const expectedMessage = "Invalid argument"
	const expectedCode int = 215

	client, mux, server := testServer()
	defer server.Close()
	mux.HandleFunc("/failure", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		fmt.Fprintf(w, `{"message": "Invalid argument", "code": 215}`)
	})

	nap := New().Client(client)
	req, _ := http.NewRequest("GET", "https://example.com/failure", nil)

	model := new(FakeModel)
	apiError := new(APIError)
	resp, err := nap.Do(req, model, apiError)

	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if resp.StatusCode != 400 {
		t.Errorf("expected %d, got %d", 400, resp.StatusCode)
	}
	if apiError.Message != expectedMessage {
		t.Errorf("expected %s, got %s", expectedMessage, apiError.Message)
	}
	if apiError.Code != expectedCode {
		t.Errorf("expected %d, got %d", expectedCode, apiError.Code)
	}
}

func TestDo_onFailureWithNilValue(t *testing.T) {
	client, mux, server := testServer()
	defer server.Close()
	mux.HandleFunc("/failure", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(420)
		fmt.Fprintf(w, `{"message": "Enhance your calm", "code": 88}`)
	})

	nap := New().Client(client)
	req, _ := http.NewRequest("GET", "https://example.com/failure", nil)

	model := new(FakeModel)
	resp, err := nap.Do(req, model, nil)

	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if resp.StatusCode != 420 {
		t.Errorf("expected %d, got %d", 420, resp.StatusCode)
	}
	expected := &FakeModel{}
	if !reflect.DeepEqual(expected, model) {
		t.Errorf("successV should not be populated, exepcted %v, got %v", expected, model)
	}
}

func TestReceive_success_nonDefaultDecoder(t *testing.T) {
	client, mux, server := testServer()
	defer server.Close()
	mux.HandleFunc("/foo/submit", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		data := ` <response>
                        <text>Some text</text>
			<favorite_count>24</favorite_count>
			<temperature>10.5</temperature>
		</response>`
		fmt.Fprintf(w, xml.Header)
		fmt.Fprintf(w, data)
	})

	endpoint := New().Client(client).Base("https://example.com/").Path("foo/").Post("submit")

	model := new(FakeModel)
	apiError := new(APIError)
	resp, err := endpoint.Clone().ResponseDecoder(xmlResponseDecoder{}).Receive(model, apiError)

	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected %d, got %d", 200, resp.StatusCode)
	}
	expectedModel := &FakeModel{Text: "Some text", FavoriteCount: 24, Temperature: 10.5}
	if !reflect.DeepEqual(expectedModel, model) {
		t.Errorf("expected %v, got %v", expectedModel, model)
	}
	expectedAPIError := &APIError{}
	if !reflect.DeepEqual(expectedAPIError, apiError) {
		t.Errorf("failureV should be zero valued, exepcted %v, got %v", expectedAPIError, apiError)
	}
}

func TestReceive_success(t *testing.T) {
	client, mux, server := testServer()
	defer server.Close()
	mux.HandleFunc("/foo/submit", func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, "POST", r)
		assertQuery(t, map[string]string{"kind_name": "vanilla", "count": "11"}, r)
		assertPostForm(t, map[string]string{"kind_name": "vanilla", "count": "11"}, r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"text": "Some text", "favorite_count": 24}`)
	})

	endpoint := New().Client(client).Base("https://example.com/").Path("foo/").Post("submit")
	// encode url-tagged struct in query params and as post body for testing purposes
	params := FakeParams{KindName: "vanilla", Count: 11}
	model := new(FakeModel)
	apiError := new(APIError)
	resp, err := endpoint.Clone().QueryStruct(params).BodyForm(params).Receive(model, apiError)

	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected %d, got %d", 200, resp.StatusCode)
	}
	expectedModel := &FakeModel{Text: "Some text", FavoriteCount: 24}
	if !reflect.DeepEqual(expectedModel, model) {
		t.Errorf("expected %v, got %v", expectedModel, model)
	}
	expectedAPIError := &APIError{}
	if !reflect.DeepEqual(expectedAPIError, apiError) {
		t.Errorf("failureV should be zero valued, exepcted %v, got %v", expectedAPIError, apiError)
	}
}

func TestReceive_failure(t *testing.T) {
	client, mux, server := testServer()
	defer server.Close()
	mux.HandleFunc("/foo/submit", func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, "POST", r)
		assertQuery(t, map[string]string{"kind_name": "vanilla", "count": "11"}, r)
		assertPostForm(t, map[string]string{"kind_name": "vanilla", "count": "11"}, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		fmt.Fprintf(w, `{"message": "Rate limit exceeded", "code": 88}`)
	})

	endpoint := New().Client(client).Base("https://example.com/").Path("foo/").Post("submit")
	// encode url-tagged struct in query params and as post body for testing purposes
	params := FakeParams{KindName: "vanilla", Count: 11}
	model := new(FakeModel)
	apiError := new(APIError)
	resp, err := endpoint.Clone().QueryStruct(params).BodyForm(params).Receive(model, apiError)

	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if resp.StatusCode != 429 {
		t.Errorf("expected %d, got %d", 429, resp.StatusCode)
	}
	expectedAPIError := &APIError{Message: "Rate limit exceeded", Code: 88}
	if !reflect.DeepEqual(expectedAPIError, apiError) {
		t.Errorf("expected %v, got %v", expectedAPIError, apiError)
	}
	expectedModel := &FakeModel{}
	if !reflect.DeepEqual(expectedModel, model) {
		t.Errorf("successV should not be zero valued, expected %v, got %v", expectedModel, model)
	}
}

func TestReceive_noContent(t *testing.T) {
	client, mux, server := testServer()
	defer server.Close()
	mux.HandleFunc("/foo/submit", func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, "HEAD", r)
		w.WriteHeader(204)
	})

	endpoint := New().Client(client).Base("https://example.com/").Path("foo/").Head("submit")
	resp, err := endpoint.Clone().Receive(nil, nil)

	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if resp.StatusCode != 204 {
		t.Errorf("expected %d, got %d", 204, resp.StatusCode)
	}
}

func TestReceive_errorCreatingRequest(t *testing.T) {
	expectedErr := errors.New("json: unsupported value: +Inf")
	resp, err := New().BodyJSON(FakeModel{Temperature: math.Inf(1)}).Receive(nil, nil)
	if err == nil || err.Error() != expectedErr.Error() {
		t.Errorf("expected %v, got %v", expectedErr, err)
	}
	if resp != nil {
		t.Errorf("expected nil resp, got %v", resp)
	}
}

func TestReuseTcpConnections(t *testing.T) {
	var connCount int32

	ln, _ := net.Listen("tcp", ":0")
	rawURL := fmt.Sprintf("https://%s/", ln.Addr())

	server := http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assertMethod(t, "GET", r)
			fmt.Fprintf(w, `{"text": "Some text"}`)
		}),
		ConnState: func(conn net.Conn, state http.ConnState) {
			if state == http.StateNew {
				atomic.AddInt32(&connCount, 1)
			}
		},
	}

	go server.Serve(ln)

	endpoint := New().Client(defaultClient).Base(rawURL).Path("foo/").Get("get")

	for i := 0; i < 10; i++ {
		resp, err := endpoint.Clone().Receive(nil, nil)
		if err != nil {
			t.Errorf("expected nil, got %v", err)
		}
		if resp.StatusCode != 200 {
			t.Errorf("expected %d, got %d", 200, resp.StatusCode)
		}
	}

	server.Shutdown(context.Background())

	if count := atomic.LoadInt32(&connCount); count != 1 {
		t.Errorf("expected 1, got %v", count)
	}
}

// Testing Utils

// testServer returns an http Client, ServeMux, and Server. The client proxies
// requests to the server and handlers can be registered on the mux to handle
// requests. The caller must close the test server.
func testServer() (*http.Client, *http.ServeMux, *httptest.Server) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	transport := &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			return url.Parse(server.URL)
		},
	}
	client := &http.Client{Transport: transport}
	return client, mux, server
}

func assertMethod(t *testing.T, expectedMethod string, req *http.Request) {
	if actualMethod := req.Method; actualMethod != expectedMethod {
		t.Errorf("expected method %s, got %s", expectedMethod, actualMethod)
	}
}

// assertQuery tests that the Request has the expected url query key/val pairs
func assertQuery(t *testing.T, expected map[string]string, req *http.Request) {
	queryValues := req.URL.Query() // net/url Values is a map[string][]string
	expectedValues := url.Values{}
	for key, value := range expected {
		expectedValues.Add(key, value)
	}
	if !reflect.DeepEqual(expectedValues, queryValues) {
		t.Errorf("expected parameters %v, got %v", expected, req.URL.RawQuery)
	}
}

// assertPostForm tests that the Request has the expected key values pairs url
// encoded in its Body
func assertPostForm(t *testing.T, expected map[string]string, req *http.Request) {
	req.ParseForm() // parses request Body to put url.Values in r.Form/r.PostForm
	expectedValues := url.Values{}
	for key, value := range expected {
		expectedValues.Add(key, value)
	}
	if !reflect.DeepEqual(expectedValues, req.PostForm) {
		t.Errorf("expected parameters %v, got %v", expected, req.PostForm)
	}
}
