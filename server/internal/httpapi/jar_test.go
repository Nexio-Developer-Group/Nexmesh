package httpapi_test

import "net/http/cookiejar"

func cookiejarNew() (*cookiejar.Jar, error) { return cookiejar.New(nil) }
