package functional

import (
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/coreos/go-oidc/key"
	"github.com/coreos/go-oidc/oidc"
	"github.com/go-gorp/gorp"
	"github.com/kylelemons/godebug/pretty"

	"github.com/coreos/dex/client"
	"github.com/coreos/dex/client/manager"
	"github.com/coreos/dex/db"
	"github.com/coreos/dex/session"
)

func connect(t *testing.T) *gorp.DbMap {
	dsn := os.Getenv("DEX_TEST_DSN")
	if dsn == "" {
		t.Fatal("Unable to proceed with empty env var DEX_TEST_DSN")
	}
	c, err := db.NewConnection(db.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("Unable to connect to database: %v", err)
	}
	if err = c.DropTablesIfExists(); err != nil {
		t.Fatalf("Unable to drop database tables: %v", err)
	}

	if err = db.DropMigrationsTable(c); err != nil {
		t.Fatalf("Unable to drop migration table: %v", err)
	}

	if _, err = db.MigrateToLatest(c); err != nil {
		t.Fatalf("Unable to migrate: %v", err)
	}

	return c
}

func TestDBSessionKeyRepoPushPop(t *testing.T) {
	r := db.NewSessionKeyRepo(connect(t))

	key := "123"
	sessionID := "456"

	r.Push(session.SessionKey{Key: key, SessionID: sessionID}, time.Second)

	got, err := r.Pop(key)
	if err != nil {
		t.Fatalf("Expected nil error: %v", err)
	}
	if got != sessionID {
		t.Fatalf("Incorrect sessionID: want=%s got=%s", sessionID, got)
	}

	// attempting to Pop a second time must fail
	if _, err := r.Pop(key); err == nil {
		t.Fatalf("Second call to Pop succeeded, expected non-nil error")
	}
}

func TestDBSessionRepoCreateUpdate(t *testing.T) {
	r := db.NewSessionRepo(connect(t))

	// postgres stores its time type with a lower precision
	// than we generate here. Stripping off nanoseconds gives
	// us a predictable value to use in comparisions.
	now := time.Now().Round(time.Second).UTC()

	ses := session.Session{
		ID:          "AAA",
		State:       session.SessionStateIdentified,
		CreatedAt:   now,
		ExpiresAt:   now.Add(time.Minute),
		ClientID:    "ZZZ",
		ClientState: "foo",
		RedirectURL: url.URL{
			Scheme: "http",
			Host:   "example.com",
			Path:   "/callback",
		},
		Identity: oidc.Identity{
			ID:        "YYY",
			Name:      "Elroy",
			Email:     "elroy@example.com",
			ExpiresAt: now.Add(time.Minute),
		},
	}

	if err := r.Create(ses); err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	got, err := r.Get(ses.ID)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if diff := pretty.Compare(ses, got); diff != "" {
		t.Fatalf("Retrieved incorrect Session: Compare(want,got): %v", diff)
	}
}

func TestDBPrivateKeySetRepoSetGet(t *testing.T) {
	s1 := []byte("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
	s2 := []byte("oooooooooooooooooooooooooooooooo")
	s3 := []byte("wwwwwwwwwwwwwwwwwwwwwwwwwwwwwwww")

	keys := []*key.PrivateKey{}
	for i := 0; i < 2; i++ {
		k, err := key.GeneratePrivateKey()
		if err != nil {
			t.Fatalf("Unable to generate RSA key: %v", err)
		}
		keys = append(keys, k)
	}

	ks := key.NewPrivateKeySet(
		[]*key.PrivateKey{keys[0], keys[1]}, time.Now().Add(time.Minute))

	tests := []struct {
		setSecrets [][]byte
		getSecrets [][]byte
		wantErr    bool
	}{
		{
			// same secrets used to encrypt, decrypt
			setSecrets: [][]byte{s1, s2},
			getSecrets: [][]byte{s1, s2},
		},
		{
			// setSecrets got rotated, but getSecrets didn't yet.
			setSecrets: [][]byte{s2, s3},
			getSecrets: [][]byte{s1, s2},
		},
		{
			// getSecrets doesn't have s3
			setSecrets: [][]byte{s3},
			getSecrets: [][]byte{s1, s2},
			wantErr:    true,
		},
	}

	for i, tt := range tests {
		dbMap := connect(t)
		setRepo, err := db.NewPrivateKeySetRepo(dbMap, false, tt.setSecrets...)
		if err != nil {
			t.Fatalf(err.Error())
		}

		getRepo, err := db.NewPrivateKeySetRepo(dbMap, false, tt.getSecrets...)
		if err != nil {
			t.Fatalf(err.Error())
		}

		if err := setRepo.Set(ks); err != nil {
			t.Fatalf("case %d: Unexpected error: %v", i, err)
		}

		got, err := getRepo.Get()
		if tt.wantErr {
			if err == nil {
				t.Errorf("case %d: want err, got nil", i)
			}
			continue
		}
		if err != nil {
			t.Fatalf("case %d: Unexpected error: %v", i, err)
		}

		if diff := pretty.Compare(ks, got); diff != "" {
			t.Fatalf("case %d:Retrieved incorrect KeySet: Compare(want,got): %v", i, diff)
		}

	}
}

func TestDBClientRepoMetadata(t *testing.T) {
	r := db.NewClientRepo(connect(t))

	cm := oidc.ClientMetadata{
		RedirectURIs: []url.URL{
			url.URL{Scheme: "http", Host: "127.0.0.1:5556", Path: "/cb"},
			url.URL{Scheme: "https", Host: "example.com", Path: "/callback"},
		},
	}

	_, err := r.New(nil, client.Client{
		Credentials: oidc.ClientCredentials{
			ID: "foo",
		},
		Metadata: cm,
	})
	if err != nil {
		t.Fatalf(err.Error())
	}

	got, err := r.Get(nil, "foo")
	if err != nil {
		t.Fatalf(err.Error())
	}

	if diff := pretty.Compare(cm, got.Metadata); diff != "" {
		t.Fatalf("Retrieved incorrect ClientMetadata: Compare(want,got): %v", diff)
	}
}

func TestDBClientRepoMetadataNoExist(t *testing.T) {
	c := connect(t)
	r := db.NewClientRepo(c)
	m := manager.NewClientManager(r, db.TransactionFactory(c), manager.ManagerOptions{})

	got, err := m.Metadata("noexist")
	if err != client.ErrorNotFound {
		t.Errorf("want==%q, got==%q", client.ErrorNotFound, err)
	}
	if got != nil {
		t.Fatalf("Retrieved incorrect ClientMetadata: want=nil got=%#v", got)
	}
}

func TestDBClientRepoNewDuplicate(t *testing.T) {
	r := db.NewClientRepo(connect(t))

	meta1 := oidc.ClientMetadata{
		RedirectURIs: []url.URL{
			url.URL{Scheme: "http", Host: "foo.example.com"},
		},
	}

	if _, err := r.New(nil, client.Client{
		Credentials: oidc.ClientCredentials{
			ID: "foo",
		},
		Metadata: meta1,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	meta2 := oidc.ClientMetadata{
		RedirectURIs: []url.URL{
			url.URL{Scheme: "http", Host: "bar.example.com"},
		},
	}

	if _, err := r.New(nil, client.Client{
		Credentials: oidc.ClientCredentials{
			ID: "foo",
		},
		Metadata: meta2,
	}); err == nil {
		t.Fatalf("expected non-nil error")
	}
}

func TestDBClientRepoNewAdmin(t *testing.T) {

	for _, admin := range []bool{true, false} {
		r := db.NewClientRepo(connect(t))
		if _, err := r.New(nil, client.Client{
			Credentials: oidc.ClientCredentials{
				ID: "foo",
			},
			Metadata: oidc.ClientMetadata{
				RedirectURIs: []url.URL{
					url.URL{Scheme: "http", Host: "foo.example.com"},
				},
			},
			Admin: admin,
		}); err != nil {
			t.Fatalf("expected non-nil error: %v", err)
		}

		gotAdmin, err := r.Get(nil, "foo")
		if err != nil {
			t.Fatalf("expected non-nil error")
		}
		if gotAdmin.Admin != admin {
			t.Errorf("want=%v, gotAdmin=%v", admin, gotAdmin)
		}

		cli, err := r.Get(nil, "foo")
		if err != nil {
			t.Fatalf("expected non-nil error")
		}
		if cli.Admin != admin {
			t.Errorf("want=%v, cli.Admin=%v", admin, cli.Admin)
		}
	}

}
func TestDBClientRepoAuthenticate(t *testing.T) {
	c := connect(t)
	r := db.NewClientRepo(c)

	clientIDGenerator := func(hostport string) (string, error) {
		return hostport, nil
	}
	secGen := func() ([]byte, error) {
		return []byte("secret"), nil
	}
	m := manager.NewClientManager(r, db.TransactionFactory(c), manager.ManagerOptions{ClientIDGenerator: clientIDGenerator, SecretGenerator: secGen})

	cm := oidc.ClientMetadata{
		RedirectURIs: []url.URL{
			url.URL{Scheme: "http", Host: "127.0.0.1:5556", Path: "/cb"},
		},
	}
	cli := client.Client{
		Metadata: cm,
	}
	cc, err := m.New(cli, nil)
	if err != nil {
		t.Fatalf(err.Error())
	}

	if cc.ID != "127.0.0.1:5556" {
		t.Fatalf("Returned ClientCredentials has incorrect ID: want=baz got=%s", cc.ID)
	}

	ok, err := m.Authenticate(*cc)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	} else if !ok {
		t.Fatalf("Authentication failed for good creds")
	}

	creds := []oidc.ClientCredentials{
		// completely made up
		oidc.ClientCredentials{ID: "foo", Secret: "bar"},

		// good client ID, bad secret
		oidc.ClientCredentials{ID: cc.ID, Secret: "bar"},

		// bad client ID, good secret
		oidc.ClientCredentials{ID: "foo", Secret: cc.Secret},

		// good client ID, secret with some fluff on the end
		oidc.ClientCredentials{ID: cc.ID, Secret: fmt.Sprintf("%sfluff", cc.Secret)},
	}
	for i, c := range creds {
		ok, err := m.Authenticate(c)
		if err != nil {
			t.Errorf("case %d: unexpected error: %v", i, err)
		} else if ok {
			t.Errorf("case %d: authentication succeeded for bad creds", i)
		}
	}
}

func TestDBClientAll(t *testing.T) {
	r := db.NewClientRepo(connect(t))

	cm := oidc.ClientMetadata{
		RedirectURIs: []url.URL{
			url.URL{Scheme: "http", Host: "127.0.0.1:5556", Path: "/cb"},
		},
	}

	_, err := r.New(nil, client.Client{
		Credentials: oidc.ClientCredentials{
			ID: "foo",
		},
		Metadata: cm,
	})
	if err != nil {
		t.Fatalf(err.Error())
	}

	got, err := r.All(nil)
	if err != nil {
		t.Fatalf(err.Error())
	}
	count := len(got)
	if count != 1 {
		t.Fatalf("Retrieved incorrect number of ClientIdentities: want=1 got=%d", count)
	}

	if diff := pretty.Compare(cm, got[0].Metadata); diff != "" {
		t.Fatalf("Retrieved incorrect ClientMetadata: Compare(want,got): %v", diff)
	}

	cm = oidc.ClientMetadata{
		RedirectURIs: []url.URL{
			url.URL{Scheme: "http", Host: "foo.com", Path: "/cb"},
		},
	}
	_, err = r.New(nil, client.Client{
		Credentials: oidc.ClientCredentials{
			ID: "bar",
		},
		Metadata: cm,
	})
	if err != nil {
		t.Fatalf(err.Error())
	}

	got, err = r.All(nil)
	if err != nil {
		t.Fatalf(err.Error())
	}
	count = len(got)
	if count != 2 {
		t.Fatalf("Retrieved incorrect number of ClientIdentities: want=2 got=%d", count)
	}
}
