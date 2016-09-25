package client

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	"github.com/coreos/go-oidc/oidc"
	"github.com/kylelemons/godebug/pretty"
)

var (
	goodSecret1 = base64.URLEncoding.EncodeToString([]byte("my_secret"))
	goodSecret2 = base64.URLEncoding.EncodeToString([]byte("my_other_secret"))
	goodSecret3 = base64.URLEncoding.EncodeToString([]byte("yet_another_secret"))

	goodClient1 = `{ 
  "id": "my_id",
  "secret": "` + goodSecret1 + `",
  "redirectURLs": ["https://client.example.com"],
  "admin": true
}`

	goodClient2 = `{ 
  "id": "my_other_id",
  "secret": "` + goodSecret2 + `",
  "redirectURLs": ["https://client2.example.com","https://client2_a.example.com"]
}`

	goodClient3 = `{ 
  "id": "yet_another_id",
  "secret": "` + goodSecret3 + `",
  "redirectURLs": ["https://client3.example.com","https://client3_a.example.com"],
  "trustedPeers":["goodClient1", "goodClient2"]
}`

	publicClient = `{ 
  "id": "public_client",
  "secret": "` + goodSecret3 + `",
  "redirectURLs": ["http://localhost:8080","urn:ietf:wg:oauth:2.0:oob"],
  "public": true
}`

	badURLClient = `{ 
  "id": "my_id",
  "secret": "` + goodSecret1 + `",
  "redirectURLs": ["hdtp:/\(bad)(u)(r)(l)"]
}`

	badSecretClient = `{ 
  "id": "my_id",
  "secret": "` + "" + `",
  "redirectURLs": ["https://client.example.com"]
}`

	noSecretClient = `{ 
  "id": "my_id",
  "redirectURLs": ["https://client.example.com"]
}`
	noIDClient = `{ 
  "secret": "` + goodSecret1 + `",
  "redirectURLs": ["https://client.example.com"]
}`
)

func TestClientsFromReader(t *testing.T) {
	tests := []struct {
		json    string
		want    []LoadableClient
		wantErr bool
	}{
		{
			json: "[]",
			want: []LoadableClient{},
		},
		{
			json: "[" + goodClient1 + "]",
			want: []LoadableClient{
				{
					Client: Client{
						Credentials: oidc.ClientCredentials{
							ID:     "my_id",
							Secret: goodSecret1,
						},
						Metadata: oidc.ClientMetadata{
							RedirectURIs: []url.URL{
								mustParseURL(t, "https://client.example.com"),
							},
						},
						Admin: true,
					},
				},
			},
		},
		{
			json: "[" + strings.Join([]string{goodClient1, goodClient2}, ",") + "]",
			want: []LoadableClient{
				{
					Client: Client{
						Credentials: oidc.ClientCredentials{
							ID:     "my_id",
							Secret: goodSecret1,
						},
						Metadata: oidc.ClientMetadata{
							RedirectURIs: []url.URL{
								mustParseURL(t, "https://client.example.com"),
							},
						},
						Admin: true,
					},
				},
				{
					Client: Client{
						Credentials: oidc.ClientCredentials{
							ID:     "my_other_id",
							Secret: goodSecret2,
						},
						Metadata: oidc.ClientMetadata{
							RedirectURIs: []url.URL{
								mustParseURL(t, "https://client2.example.com"),
								mustParseURL(t, "https://client2_a.example.com"),
							},
						},
					},
				},
			},
		},
		{
			json: "[" + goodClient3 + "]",
			want: []LoadableClient{
				{
					Client: Client{
						Credentials: oidc.ClientCredentials{
							ID:     "yet_another_id",
							Secret: goodSecret3,
						},
						Metadata: oidc.ClientMetadata{
							RedirectURIs: []url.URL{
								mustParseURL(t, "https://client3.example.com"),
								mustParseURL(t, "https://client3_a.example.com"),
							},
						},
					},
					TrustedPeers: []string{"goodClient1", "goodClient2"},
				},
			},
		},
		{
			json: "[" + publicClient + "]",
			want: []LoadableClient{
				{
					Client: Client{
						Credentials: oidc.ClientCredentials{
							ID:     "public_client",
							Secret: goodSecret3,
						},
						Metadata: oidc.ClientMetadata{
							RedirectURIs: []url.URL{
								mustParseURL(t, "http://localhost:8080"),
								mustParseURL(t, "urn:ietf:wg:oauth:2.0:oob"),
							},
						},
						Public: true,
					},
				},
			},
		},
		{
			json:    "[" + badURLClient + "]",
			wantErr: true,
		},
		{
			json:    "[" + badSecretClient + "]",
			wantErr: true,
		},
		{
			json:    "[" + noSecretClient + "]",
			wantErr: true,
		},
		{
			json:    "[" + noIDClient + "]",
			wantErr: true,
		},
	}

	for i, tt := range tests {
		r := strings.NewReader(tt.json)
		cs, err := ClientsFromReader(r)
		if tt.wantErr {
			if err == nil {
				t.Errorf("case %d: want non-nil err", i)
				t.Logf(pretty.Sprint(cs))
			}
			continue
		}
		if err != nil {
			t.Errorf("case %d: got unexpected error parsing clients: %v", i, err)
			t.Logf(tt.json)
		}

		if diff := pretty.Compare(tt.want, cs); diff != "" {
			t.Errorf("case %d: Compare(want, got): %v", i, diff)
		}
	}
}
func TestClientValidRedirectURL(t *testing.T) {
	makeClient := func(public bool, urls []string) Client {
		cli := Client{
			Metadata: oidc.ClientMetadata{
				RedirectURIs: make([]url.URL, len(urls)),
			},
			Public: public,
		}
		for i, s := range urls {
			cli.Metadata.RedirectURIs[i] = mustParseURL(t, s)
		}
		return cli
	}

	tests := []struct {
		u   string
		cli Client

		wantU   string
		wantErr bool
	}{
		{
			u:     "http://auth.example.com",
			cli:   makeClient(false, []string{"http://auth.example.com"}),
			wantU: "http://auth.example.com",
		},
		{
			u:     "http://auth2.example.com",
			cli:   makeClient(false, []string{"http://auth.example.com", "http://auth2.example.com"}),
			wantU: "http://auth2.example.com",
		},
		{
			u:     "",
			cli:   makeClient(false, []string{"http://auth.example.com"}),
			wantU: "http://auth.example.com",
		},
		{
			u:       "",
			cli:     makeClient(false, []string{"http://auth.example.com", "http://auth2.example.com"}),
			wantErr: true,
		},
		{
			u:     "http://localhost:8080",
			cli:   makeClient(true, []string{}),
			wantU: "http://localhost:8080",
		},
		{
			u:     OOBRedirectURI,
			cli:   makeClient(true, []string{}),
			wantU: OOBRedirectURI,
		},
		{
			u:       "",
			cli:     makeClient(true, []string{}),
			wantErr: true,
		},
		{
			u:       "http://localhost:8080/hey_there",
			cli:     makeClient(true, []string{}),
			wantErr: true,
		},
		{
			u:       "http://auth.google.com:8080",
			cli:     makeClient(true, []string{}),
			wantErr: true,
		},
	}

	for i, tt := range tests {
		var testURL *url.URL
		if tt.u == "" {
			testURL = nil
		} else {
			u := mustParseURL(t, tt.u)
			testURL = &u
		}

		u, err := tt.cli.ValidRedirectURL(testURL)
		if tt.wantErr {
			if err == nil {
				t.Errorf("case %d: want non-nil error", i)
			}
			continue
		}

		if err != nil {
			t.Errorf("case %d: unexpected error: %v", i, err)
		}

		if diff := pretty.Compare(mustParseURL(t, tt.wantU), u); diff != "" {
			t.Fatalf("case %d: Compare(wantU, u): %v", i, diff)
		}
	}

}
func mustParseURL(t *testing.T, s string) url.URL {
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("Cannot parse %v as url: %v", s, err)
	}
	return *u
}
