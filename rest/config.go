package rest

type config struct {
	// http Client for doing requests
	httpClient Doer
	// response decoder
	responseDecoder ResponseDecoder
	// func success decider
	isSuccess SuccessDecider
}

type Option interface {
	apply(*config)
}

type optionFunc func(*config)

func (o optionFunc) apply(c *config) {
	o(c)
}

func newConfig(opts ...Option) *config {
	c := &config{
		httpClient:      defaultClient,
		responseDecoder: jsonDecoder{},
		isSuccess:       DecodeOnSuccess,
	}
	for _, opt := range opts {
		opt.apply(c)
	}

	return c
}

func WithHttpClient(httpClient Doer) Option {
	return optionFunc(func(c *config) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	})
}

func WithJsonResponseDecoder() Option {
	return optionFunc(func(c *config) {
		c.responseDecoder = jsonDecoder{}
	})
}

func WithXmlResponseDecoder() Option {
	return optionFunc(func(c *config) {
		c.responseDecoder = xmlDecoder{}
	})
}

func WithResponseDecoder(decoder ResponseDecoder) Option {
	return optionFunc(func(c *config) {
		if decoder != nil {
			c.responseDecoder = decoder
		}
	})
}

func WithSuccessDecider(is SuccessDecider) Option {
	return optionFunc(func(c *config) {
		if is != nil {
			c.isSuccess = is
		}
	})
}
