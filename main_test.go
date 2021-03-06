package main

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Vertamedia/chproxy/cache"
	"github.com/Vertamedia/chproxy/config"
	"github.com/Vertamedia/chproxy/log"
)

var testDir = "./temp-test-data"

func TestMain(m *testing.M) {
	log.SuppressOutput(true)
	retCode := m.Run()
	log.SuppressOutput(false)
	if err := os.RemoveAll(testDir); err != nil {
		log.Fatalf("cannot remove %q: %s", testDir, err)
	}
	os.Exit(retCode)
}

func TestServe(t *testing.T) {
	var testCases = []struct {
		name     string
		file     string
		testFn   func(t *testing.T)
		listenFn func() (net.Listener, chan struct{})
	}{
		{
			"https request",
			"testdata/https.yml",
			func(t *testing.T) {
				req, err := http.NewRequest("GET", "https://127.0.0.1:8443?query=asd", nil)
				checkErr(t, err)
				resp, err := tlsClient.Do(req)
				checkErr(t, err)
				if resp.StatusCode != http.StatusUnauthorized {
					t.Fatalf("unexpected status code: %d; expected: %d", resp.StatusCode, http.StatusUnauthorized)
				}
				resp.Body.Close()

				req, err = http.NewRequest("GET", "https://127.0.0.1:8443?query=asd", nil)
				checkErr(t, err)
				req.SetBasicAuth("default", "qwerty")
				resp, err = tlsClient.Do(req)
				checkErr(t, err)
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("unexpected status code: %d; expected: %d", resp.StatusCode, http.StatusOK)
				}
				resp.Body.Close()
			},
			startTLS,
		},
		{
			"https cache",
			"testdata/https.cache.yml",
			func(t *testing.T) {
				// do request which response must be cached
				q := "SELECT 123"
				req, err := http.NewRequest("GET", "https://127.0.0.1:8443?query="+url.QueryEscape(q), nil)
				checkErr(t, err)
				req.SetBasicAuth("default", "qwerty")
				resp, err := tlsClient.Do(req)
				checkErr(t, err)
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("unexpected status code: %d; expected: %d", resp.StatusCode, http.StatusOK)
				}
				resp.Body.Close()

				// check cached response
				key := &cache.Key{
					Query:          []byte(q),
					AcceptEncoding: "gzip",
				}
				path := fmt.Sprintf("%s/cache/%s", testDir, key.String())
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("err while getting file %q info: %s", path, err)
				}
				rw := httptest.NewRecorder()
				cc := proxy.caches["https_cache"]
				if err := cc.WriteTo(rw, key); err != nil {
					t.Fatalf("unexpected error while writing reposnse from cache: %s", err)
				}
				expected := "Ok.\n"
				checkResponse(t, rw.Body, expected)
			},
			startTLS,
		},
		{
			"bad https cache",
			"testdata/https.cache.yml",
			func(t *testing.T) {
				// do request which cause an error
				q := "SELECT ERROR"
				req, err := http.NewRequest("GET", "https://127.0.0.1:8443?query="+url.QueryEscape(q), nil)
				checkErr(t, err)
				req.SetBasicAuth("default", "qwerty")
				resp, err := tlsClient.Do(req)
				checkErr(t, err)
				if resp.StatusCode != http.StatusTeapot {
					t.Fatalf("unexpected status code: %d; expected: %d", resp.StatusCode, http.StatusTeapot)
				}
				resp.Body.Close()

				// check cached response
				key := &cache.Key{
					Query:          []byte(q),
					AcceptEncoding: "gzip",
				}
				path := fmt.Sprintf("%s/cache/%s", testDir, key.String())
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("err while getting file %q info: %s", path, err)
				}

				// check refreshCacheMetrics()
				req, err = http.NewRequest("GET", "https://127.0.0.1:8443/metrics", nil)
				checkErr(t, err)
				resp, err = tlsClient.Do(req)
				if err != nil {
					t.Fatalf("unexpected error while doing request: %s", err)
				}
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("unexpected status code: %d; expected: %d", resp.StatusCode, http.StatusOK)
				}
				resp.Body.Close()
			},
			startTLS,
		},
		{
			"deny https",
			"testdata/https.deny.https.yml",
			func(t *testing.T) {
				req, err := http.NewRequest("GET", "https://127.0.0.1:8443?query=asd", nil)
				checkErr(t, err)
				req.SetBasicAuth("default", "qwerty")
				resp, err := tlsClient.Do(req)
				checkErr(t, err)
				if resp.StatusCode != http.StatusForbidden {
					t.Fatalf("unexpected status code: %d; expected: %d", resp.StatusCode, http.StatusForbidden)
				}
				expected := "user \"default\" is not allowed to access via https"
				checkResponse(t, resp.Body, expected)
				resp.Body.Close()
			},
			startTLS,
		},
		{
			"https networks",
			"testdata/https.networks.yml",
			func(t *testing.T) {
				req, err := http.NewRequest("GET", "https://127.0.0.1:8443?query=asd", nil)
				checkErr(t, err)
				req.SetBasicAuth("default", "qwerty")
				resp, err := tlsClient.Do(req)
				checkErr(t, err)
				if resp.StatusCode != http.StatusForbidden {
					t.Fatalf("unexpected status code: %d; expected: %d", resp.StatusCode, http.StatusForbidden)
				}
				expected := "https connections are not allowed from 127.0.0.1"
				checkResponse(t, resp.Body, expected)
				resp.Body.Close()
			},
			startTLS,
		},
		{
			"https user networks",
			"testdata/https.user.networks.yml",
			func(t *testing.T) {
				req, err := http.NewRequest("GET", "https://127.0.0.1:8443?query=asd", nil)
				checkErr(t, err)
				req.SetBasicAuth("default", "qwerty")
				resp, err := tlsClient.Do(req)
				checkErr(t, err)
				if resp.StatusCode != http.StatusForbidden {
					t.Fatalf("unexpected status code: %d; expected: %d", resp.StatusCode, http.StatusForbidden)
				}
				expected := "user \"default\" is not allowed to access"
				checkResponse(t, resp.Body, expected)
				resp.Body.Close()
			},
			startTLS,
		},
		{
			"https cluster user networks",
			"testdata/https.cluster.user.networks.yml",
			func(t *testing.T) {
				req, err := http.NewRequest("GET", "https://127.0.0.1:8443?query=asd", nil)
				checkErr(t, err)
				req.SetBasicAuth("default", "qwerty")
				resp, err := tlsClient.Do(req)
				checkErr(t, err)
				if resp.StatusCode != http.StatusForbidden {
					t.Fatalf("unexpected status code: %d; expected: %d", resp.StatusCode, http.StatusForbidden)
				}
				expected := "cluster user \"web\" is not allowed to access"
				checkResponse(t, resp.Body, expected)
				resp.Body.Close()
			},
			startTLS,
		},
		{
			"routing",
			"testdata/http.yml",
			func(t *testing.T) {
				req, err := http.NewRequest(http.MethodOptions, "http://127.0.0.1:9090?query=asd", nil)
				checkErr(t, err)
				resp, err := http.DefaultClient.Do(req)
				checkErr(t, err)
				expectedAllowHeader := "GET,POST"
				if resp.Header.Get("Allow") != expectedAllowHeader {
					t.Fatalf("header `Allow` got: %q; expected: %q", resp.Header.Get("Allow"), expectedAllowHeader)
				}
				resp.Body.Close()

				req, err = http.NewRequest(http.MethodConnect, "http://127.0.0.1:9090?query=asd", nil)
				checkErr(t, err)
				resp, err = http.DefaultClient.Do(req)
				checkErr(t, err)
				expected := fmt.Sprintf("unsupported method %q", http.MethodConnect)
				checkResponse(t, resp.Body, expected)
				if resp.StatusCode != http.StatusMethodNotAllowed {
					t.Fatalf("unexpected status code: %d; expected: %d", resp.StatusCode, http.StatusMethodNotAllowed)
				}
				resp.Body.Close()

				resp = httpGet(t, "http://127.0.0.1:9090/foobar", http.StatusBadRequest)
				expected = fmt.Sprintf("unsupported path: \"/foobar\"")
				checkResponse(t, resp.Body, expected)
				resp.Body.Close()
			},
			startHTTP,
		},
		{
			"http request",
			"testdata/http.yml",
			func(t *testing.T) {
				httpGet(t, "http://127.0.0.1:9090?query=asd", http.StatusOK)
				httpGet(t, "http://127.0.0.1:9090/metrics", http.StatusOK)
			},
			startHTTP,
		},
		{
			"http gzipped POST request",
			"testdata/http.yml",
			func(t *testing.T) {
				var buf bytes.Buffer
				zw := gzip.NewWriter(&buf)
				_, err := zw.Write([]byte("SELECT * FROM system.numbers LIMIT 10"))
				checkErr(t, err)
				zw.Close()
				req, err := http.NewRequest("POST", "http://127.0.0.1:9090", &buf)
				req.Header.Set("Content-Encoding", "gzip")
				checkErr(t, err)
				resp, err := http.DefaultClient.Do(req)
				checkErr(t, err)
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("unexpected status code: %d; expected: %d", resp.StatusCode, http.StatusOK)
				}
				resp.Body.Close()
			},
			startHTTP,
		},
		{
			"http POST request",
			"testdata/http.yml",
			func(t *testing.T) {
				buf := bytes.NewBufferString("SELECT * FROM system.numbers LIMIT 10")
				req, err := http.NewRequest("POST", "http://127.0.0.1:9090", buf)
				checkErr(t, err)
				resp, err := http.DefaultClient.Do(req)
				checkErr(t, err)
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("unexpected status code: %d; expected: %d", resp.StatusCode, http.StatusOK)
				}
				resp.Body.Close()
			},
			startHTTP,
		},
		{
			"http gzipped POST execution time",
			"testdata/http.execution.time.yml",
			func(t *testing.T) {
				var buf bytes.Buffer
				zw := gzip.NewWriter(&buf)
				_, err := zw.Write([]byte("SELECT * FROM system.numbers LIMIT 1000"))
				checkErr(t, err)
				zw.Close()
				req, err := http.NewRequest("POST", "http://127.0.0.1:9090", &buf)
				req.Header.Set("Content-Encoding", "gzip")
				checkErr(t, err)
				resp, err := http.DefaultClient.Do(req)
				checkErr(t, err)
				if resp.StatusCode != http.StatusGatewayTimeout {
					t.Fatalf("unexpected status code: %d; expected: %d", resp.StatusCode, http.StatusGatewayTimeout)
				}
				resp.Body.Close()
			},
			startHTTP,
		},
		{
			"deny http",
			"testdata/http.deny.http.yml",
			func(t *testing.T) {
				resp := httpGet(t, "http://127.0.0.1:9090?query=asd", http.StatusForbidden)
				expected := "user \"default\" is not allowed to access via http"
				checkResponse(t, resp.Body, expected)
				resp.Body.Close()
			},
			startHTTP,
		},
		{
			"http networks",
			"testdata/http.networks.yml",
			func(t *testing.T) {
				resp := httpGet(t, "http://127.0.0.1:9090?query=asd", http.StatusForbidden)
				expected := "http connections are not allowed from 127.0.0.1"
				checkResponse(t, resp.Body, expected)
				resp.Body.Close()
			},
			startHTTP,
		},
		{
			"http metrics networks",
			"testdata/http.metrics.networks.yml",
			func(t *testing.T) {
				resp := httpGet(t, "http://127.0.0.1:9090/metrics", http.StatusForbidden)
				expected := "connections to /metrics are not allowed from 127.0.0.1"
				checkResponse(t, resp.Body, expected)
				resp.Body.Close()
			},
			startHTTP,
		},
		{
			"http user networks",
			"testdata/http.user.networks.yml",
			func(t *testing.T) {
				resp := httpGet(t, "http://127.0.0.1:9090?query=asd", http.StatusForbidden)
				expected := "user \"default\" is not allowed to access"
				checkResponse(t, resp.Body, expected)
				resp.Body.Close()
			},
			startHTTP,
		},
		{
			"http cluster user networks",
			"testdata/http.cluster.user.networks.yml",
			func(t *testing.T) {
				resp := httpGet(t, "http://127.0.0.1:9090?query=asd", http.StatusForbidden)
				expected := "cluster user \"web\" is not allowed to access"
				checkResponse(t, resp.Body, expected)
				resp.Body.Close()
			},
			startHTTP,
		},
		{
			"http shared cache",
			"testdata/http.shared.cache.yml",
			func(t *testing.T) {
				// actually we can check that cache-file is shared via metrics
				// but it needs additional library for doing this
				cacheDir := "temp-test-data/shared_cache"
				checkFilesCount := func(expectedLen int) {
					files, err := ioutil.ReadDir(cacheDir)
					if err != nil {
						t.Fatalf("error while reading dir %q: %s", cacheDir, err)
					}
					if expectedLen != len(files) {
						t.Fatalf("expected %d files in cache dir; got: %d", expectedLen, len(files))
					}
				}

				checkFilesCount(0)
				httpGet(t, "http://127.0.0.1:9090?query=SELECT&user=user1", http.StatusOK)
				checkFilesCount(1)
				httpGet(t, "http://127.0.0.1:9090?query=SELECT&user=user2", http.StatusOK)
				// request from different user expected to be served with already cached,
				// so count of files should be the same
				checkFilesCount(1)
			},
			startHTTP,
		},
	}

	// Wait until CHServer starts.
	go startCHServer()
	startTime := time.Now()
	i := 0
	for i < 10 {
		if _, err := http.Get("http://127.0.0.1:8124/"); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
		i++
	}
	if i >= 10 {
		t.Fatalf("CHServer didn't start in %s", time.Since(startTime))
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			*configFile = tc.file
			ln, done := tc.listenFn()
			defer func() {
				if err := ln.Close(); err != nil {
					t.Fatalf("unexpected error while closing listener: %s", err)
				}
				<-done
			}()

			var c *cluster
			for _, cluster := range proxy.clusters {
				c = cluster
				break
			}
			var i int
			for {
				if i > 3 {
					t.Fatal("unable to find active hosts")
				}
				if h := c.getHost(); h != nil {
					break
				}
				i++
				time.Sleep(time.Millisecond * 10)
			}

			tc.testFn(t)
		})
	}
}

var tlsClient = &http.Client{Transport: &http.Transport{
	TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
}}

func startTLS() (net.Listener, chan struct{}) {
	cfg, err := loadConfig()
	if err != nil {
		panic(fmt.Sprintf("error while loading config: %s", err))
	}
	if err = applyConfig(cfg); err != nil {
		panic(fmt.Sprintf("error while applying config: %s", err))
	}
	done := make(chan struct{})
	ln, err := net.Listen("tcp4", cfg.Server.HTTPS.ListenAddr)
	if err != nil {
		panic(fmt.Sprintf("cannot listen for %q: %s", cfg.Server.HTTPS.ListenAddr, err))
	}
	tlsCfg := newTLSConfig(cfg.Server.HTTPS)
	tln := tls.NewListener(ln, tlsCfg)
	go func() {
		listenAndServe(tln, time.Minute)
		close(done)
	}()
	return tln, done
}

func startHTTP() (net.Listener, chan struct{}) {
	cfg, err := loadConfig()
	if err != nil {
		panic(fmt.Sprintf("error while loading config: %s", err))
	}
	if err = applyConfig(cfg); err != nil {
		panic(fmt.Sprintf("error while applying config: %s", err))
	}
	done := make(chan struct{})
	ln, err := net.Listen("tcp4", cfg.Server.HTTP.ListenAddr)
	if err != nil {
		panic(fmt.Sprintf("cannot listen for %q: %s", cfg.Server.HTTP.ListenAddr, err))
	}
	go func() {
		listenAndServe(ln, time.Minute)
		close(done)
	}()
	return ln, done
}

func startCHServer() {
	http.ListenAndServe(":8124", http.HandlerFunc(fakeHandler))
}

func fakeHandler(w http.ResponseWriter, r *http.Request) {
	params := r.URL.Query()
	query := params.Get("query")
	switch query {
	case "SELECT ERROR":
		w.WriteHeader(http.StatusTeapot)
		fmt.Fprint(w, "DB::Exception\n")
	default:
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "Ok.\n")
	}
}

func TestNewTLSConfig(t *testing.T) {
	cfg := config.HTTPS{
		KeyFile:  "testdata/example.com.key",
		CertFile: "testdata/example.com.cert",
	}

	tlsCfg := newTLSConfig(cfg)
	if len(tlsCfg.Certificates) < 1 {
		t.Fatalf("expected tls certificate; got empty list")
	}

	certCachePath := fmt.Sprintf("%s/certs_dir", testDir)
	cfg = config.HTTPS{
		Autocert: config.Autocert{
			CacheDir:     certCachePath,
			AllowedHosts: []string{"example.com"},
		},
	}
	tlsCfg = newTLSConfig(cfg)
	if tlsCfg.GetCertificate == nil {
		t.Fatalf("expected func GetCertificate be set; got nil")
	}

	if _, err := os.Stat(certCachePath); err != nil {
		t.Fatalf("expected dir %s to be created", certCachePath)
	}
}

func TestGetMaxResponseTime(t *testing.T) {
	cfg := &config.Config{
		Clusters: []config.Cluster{
			{
				ClusterUsers: []config.ClusterUser{
					{
						MaxExecutionTime: 20 * time.Second,
					},
				},
			},
			{
				ClusterUsers: []config.ClusterUser{
					{
						MaxExecutionTime: 30 * time.Second,
					},
					{
						MaxExecutionTime: 10 * time.Second,
					},
				},
			},
		},
		Users: []config.User{
			{
				MaxExecutionTime: 10 * time.Second,
			},
			{
				MaxExecutionTime: 15 * time.Second,
			},
		},
	}

	expected := 30 * time.Second
	if maxTime := getMaxResponseTime(cfg); maxTime != expected {
		t.Fatalf("got %v; expected %v", maxTime, expected)
	}

	expected = time.Minute
	cfg.Users[0].MaxExecutionTime = expected
	if maxTime := getMaxResponseTime(cfg); maxTime != expected {
		t.Fatalf("got %v; expected %v", maxTime, expected)
	}
}

func TestReloadConfig(t *testing.T) {
	*configFile = "testdata/http.yml"
	if err := reloadConfig(); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	*configFile = "testdata/foobar.yml"
	if err := reloadConfig(); err == nil {
		t.Fatal("error expected; got nil")
	}
}

func checkErr(t *testing.T, err error) {
	if err != nil {
		t.Fatalf("unexpected erorr: %s", err)
	}
}

func checkResponse(t *testing.T, r io.Reader, expected string) {
	response, err := ioutil.ReadAll(r)
	if err != nil {
		t.Fatalf("unexpected err while reading: %s", err)
	}
	got := string(response)
	if !strings.Contains(got, expected) {
		t.Fatalf("got: %q; expected: %q", got, expected)
	}
}

func httpGet(t *testing.T, url string, statusCode int) *http.Response {
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("unexpected erorr while doing GET request: %s", err)
	}
	if resp.StatusCode != statusCode {
		t.Fatalf("unexpected status code: %d; expected: %d", resp.StatusCode, statusCode)
	}
	return resp
}
