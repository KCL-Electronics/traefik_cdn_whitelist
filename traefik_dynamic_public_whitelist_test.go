package traefik_dynamic_public_whitelist_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	traefikdynamicpublicwhitelist "github.com/KCL-Electronics/traefik_cdn_whitelist"
	"github.com/traefik/genconf/dynamic"
	"github.com/traefik/genconf/dynamic/tls"
)

func TestProvideCustomProvider(t *testing.T) {
	mockRequestV4 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte("192.0.2.123"))
		if err != nil {
			return
		}
	}))
	t.Cleanup(mockRequestV4.Close)

	mockRequestV6 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte("1234:1234:1234:1234:1234:1234:1234:1234"))
		if err != nil {
			return
		}
	}))
	t.Cleanup(mockRequestV6.Close)

	config := traefikdynamicpublicwhitelist.CreateConfig()
	config.Provider = traefikdynamicpublicwhitelist.ProviderCustom
	config.PollInterval = "1s"
	config.IPv4Resolver = mockRequestV4.URL
	config.IPv6Resolver = mockRequestV6.URL
	config.WhitelistIPv6 = true
	config.AdditionalSourceRange = []string{"127.0.0.1/32", "192.168.0.24"}
	config.IPStrategy = dynamic.IPStrategy{Depth: 1, ExcludedIPs: []string{"123.0.0.1"}}

	provider := newProvider(t, config)
	t.Cleanup(func() {
		if err := provider.Stop(); err != nil {
			t.Fatalf("stop provider: %v", err)
		}
	})
	cfgChan := make(chan json.Marshaler)
	if err := provider.Provide(cfgChan); err != nil {
		t.Fatal(err)
	}

	data := <-cfgChan

	expected := &dynamic.Configuration{
		HTTP: &dynamic.HTTPConfiguration{
			Routers:  map[string]*dynamic.Router{},
			Services: map[string]*dynamic.Service{},
			Middlewares: map[string]*dynamic.Middleware{
				"public_ipwhitelist": {
					IPWhiteList: &dynamic.IPWhiteList{
						SourceRange: []string{"127.0.0.1/32", "192.168.0.24", "192.0.2.123", "1234:1234:1234:1234::/64"},
						IPStrategy:  &dynamic.IPStrategy{Depth: 1, ExcludedIPs: []string{"123.0.0.1"}},
					},
				},
			},
			ServersTransports: map[string]*dynamic.ServersTransport{},
		},
		TCP: &dynamic.TCPConfiguration{Routers: map[string]*dynamic.TCPRouter{}, Services: map[string]*dynamic.TCPService{}},
		TLS: &dynamic.TLSConfiguration{Stores: map[string]tls.Store{}, Options: map[string]tls.Options{}},
		UDP: &dynamic.UDPConfiguration{Routers: map[string]*dynamic.UDPRouter{}, Services: map[string]*dynamic.UDPService{}},
	}

	expectedJSON, err := json.MarshalIndent(expected, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	dataJSON, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(expectedJSON, dataJSON) {
		t.Fatalf("got %s, want: %s", string(dataJSON), string(expectedJSON))
	}
}

func TestProviderRequiresName(t *testing.T) {
	cfg := traefikdynamicpublicwhitelist.CreateConfig()
	cfg.Provider = ""
	if _, err := traefikdynamicpublicwhitelist.New(context.Background(), cfg, "test"); err == nil {
		t.Fatal("expected error when provider missing")
	}
}

func TestCustomProviderRequiresResolvers(t *testing.T) {
	cfg := baseConfig(traefikdynamicpublicwhitelist.ProviderCustom)
	cfg.IPv4Resolver = ""
	if _, err := traefikdynamicpublicwhitelist.New(context.Background(), cfg, "test"); err == nil {
		t.Fatal("expected error when ipv4Resolver missing")
	}
}

func TestCloudflareProvider(t *testing.T) {
	ipv4Data := "198.51.100.0/24\n203.0.113.0/25"
	ipv6Data := "2001:db8::/32\n2001:db8:1::/48"

	v4Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertHeader(t, r, "X-Kes-RequestID")
		_, err := w.Write([]byte(ipv4Data))
		if err != nil {
			return
		}
	}))
	t.Cleanup(v4Srv.Close)

	v6Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertHeader(t, r, "X-Kes-RequestID")
		_, err := w.Write([]byte(ipv6Data))
		if err != nil {
			return
		}
	}))
	t.Cleanup(v6Srv.Close)

	traefikdynamicpublicwhitelist.SetCloudflareEndpoints(v4Srv.URL, v6Srv.URL)
	t.Cleanup(func() {
		traefikdynamicpublicwhitelist.SetCloudflareEndpoints(
			"https://www.cloudflare.com/ips-v4/",
			"https://www.cloudflare.com/ips-v6/",
		)
	})

	config := baseConfig(traefikdynamicpublicwhitelist.ProviderCloudflare)
	config.WhitelistIPv6 = true

	cfg := loadOnce(t, config)

	got := cfg.HTTP.Middlewares["public_ipwhitelist"].IPWhiteList.SourceRange
	expected := []string{"198.51.100.0/24", "203.0.113.0/25", "2001:db8::/32", "2001:db8:1::/48"}

	if strings.Join(got, ",") != strings.Join(expected, ",") {
		t.Fatalf("unexpected source ranges: %v", got)
	}
}

func TestFastlyProvider(t *testing.T) {
	payload := `{"addresses":["198.51.100.0/24"],"ipv6_addresses":["2001:db8::/48"]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertHeader(t, r, "X-Kes-RequestID")
		_, err := w.Write([]byte(payload))
		if err != nil {
			return
		}
	}))
	t.Cleanup(srv.Close)

	traefikdynamicpublicwhitelist.SetFastlyEndpoint(srv.URL)
	t.Cleanup(func() {
		traefikdynamicpublicwhitelist.SetFastlyEndpoint("https://api.fastly.com/public-ip-list")
	})

	config := baseConfig(traefikdynamicpublicwhitelist.ProviderFastly)
	config.WhitelistIPv6 = true

	cfg := loadOnce(t, config)

	got := cfg.HTTP.Middlewares["public_ipwhitelist"].IPWhiteList.SourceRange
	if len(got) != 2 {
		t.Fatalf("expected two ranges, got %v", got)
	}
}

func TestCloudfrontProvider(t *testing.T) {
	payload := `{"prefixes":[{"ip_prefix":"198.51.100.0/24","service":"CLOUDFRONT"}],` +
		`"ipv6_prefixes":[{"ipv6_prefix":"2001:db8::/48","service":"CLOUDFRONT"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertHeader(t, r, "X-Kes-RequestID")
		_, err := w.Write([]byte(payload))
		if err != nil {
			return
		}
	}))
	t.Cleanup(srv.Close)

	traefikdynamicpublicwhitelist.SetAwsIPRangesEndpoint(srv.URL)
	t.Cleanup(func() {
		traefikdynamicpublicwhitelist.SetAwsIPRangesEndpoint("https://ip-ranges.amazonaws.com/ip-ranges.json")
	})

	config := baseConfig(traefikdynamicpublicwhitelist.ProviderCloudfront)
	config.WhitelistIPv6 = true

	cfg := loadOnce(t, config)

	got := cfg.HTTP.Middlewares["public_ipwhitelist"].IPWhiteList.SourceRange
	if len(got) != 2 {
		t.Fatalf("expected two ranges, got %v", got)
	}
}

func baseConfig(provider string) *traefikdynamicpublicwhitelist.Config {
	cfg := traefikdynamicpublicwhitelist.CreateConfig()
	cfg.Provider = provider
	cfg.PollInterval = "1s"
	return cfg
}

func newProvider(t *testing.T, cfg *traefikdynamicpublicwhitelist.Config) *traefikdynamicpublicwhitelist.Provider {
	t.Helper()

	provider, err := traefikdynamicpublicwhitelist.New(context.Background(), cfg, "test")
	if err != nil {
		t.Fatal(err)
	}

	if err := provider.Init(); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := provider.Stop(); err != nil {
			t.Fatalf("stop provider: %v", err)
		}
	})

	return provider
}

func loadOnce(t *testing.T, cfg *traefikdynamicpublicwhitelist.Config) *dynamic.Configuration {
	t.Helper()

	provider := newProvider(t, cfg)
	configuration, err := provider.GenerateConfiguration(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	return configuration
}

func assertHeader(t *testing.T, r *http.Request, name string) {
	t.Helper()
	if val := r.Header.Get(name); val == "" {
		t.Fatalf("expected header %s", name)
	}
}
