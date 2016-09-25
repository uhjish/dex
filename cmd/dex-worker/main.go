package main

import (
	"expvar"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/coreos/pkg/flagutil"
	"github.com/gorilla/handlers"

	"github.com/coreos/dex/connector"
	"github.com/coreos/dex/db"
	pflag "github.com/coreos/dex/pkg/flag"
	"github.com/coreos/dex/pkg/log"
	ptime "github.com/coreos/dex/pkg/time"
	"github.com/coreos/dex/server"
)

var version = "DEV"

func init() {
	versionVar := expvar.NewString("dex.version")
	versionVar.Set(version)
}

func main() {
	fs := flag.NewFlagSet("dex-worker", flag.ExitOnError)
	listen := fs.String("listen", "http://127.0.0.1:5556", "the address that the server will listen on")

	issuer := fs.String("issuer", "http://127.0.0.1:5556/dex", "the issuer's location")

	certFile := fs.String("tls-cert-file", "", "the server's certificate file for TLS connection")
	keyFile := fs.String("tls-key-file", "", "the server's private key file for TLS connection")

	templates := fs.String("html-assets", "./static/html", "directory of html template files")

	emailTemplateDirs := flagutil.StringSliceFlag{"./static/email"}
	fs.Var(&emailTemplateDirs, "email-templates", "comma separated list of directories of email template files")

	emailFrom := fs.String("email-from", "", `DEPRICATED: use "from" field in email config.`)
	emailConfig := fs.String("email-cfg", "./static/fixtures/emailer.json", "configures emailer.")

	enableRegistration := fs.Bool("enable-registration", false, "Allows users to self-register. This flag cannot be used in combination with --enable-automatic-registration.")
	registerOnFirstLogin := fs.Bool("enable-automatic-registration", false, "When a user logs in through a federated identity service, automatically register them if they don't have an account. This flag cannot be used in combination with --enable-registration.")

	enableClientRegistration := fs.Bool("enable-client-registration", false, "Allow dynamic registration of clients")

	// Client credentials administration
	apiUseClientCredentials := fs.Bool("api-use-client-credentials", false, "Forces API to authenticate using client credentials instead of ID token. Clients must be 'admin clients' to use the API.")

	noDB := fs.Bool("no-db", false, "manage entities in-process w/o any encryption, used only for single-node testing")

	// UI-related:
	issuerName := fs.String("issuer-name", "dex", "The name of this dex installation; will appear on most pages.")
	issuerLogoURL := fs.String("issuer-logo-url", "https://coreos.com/assets/images/brand/coreos-wordmark-135x40px.png", "URL of an image representing the issuer")

	// ignored if --no-db is set
	dbURL := fs.String("db-url", "", "DSN-formatted database connection string")

	keySecrets := pflag.NewBase64List(32)
	fs.Var(keySecrets, "key-secrets", "A comma-separated list of base64 encoded 32 byte strings used as symmetric keys used to encrypt/decrypt signing key data in DB. The first key is considered the active key and used for encryption, while the others are used to decrypt.")

	useOldFormat := fs.Bool("use-deprecated-secret-format", false, "In prior releases, the database used AES-CBC to encrypt keys. New deployments should use the default AES-GCM encryption.")

	dbMaxIdleConns := fs.Int("db-max-idle-conns", 0, "maximum number of connections in the idle connection pool")
	dbMaxOpenConns := fs.Int("db-max-open-conns", 0, "maximum number of open connections to the database")
	printVersion := fs.Bool("version", false, "Print the version and exit")

	// These are configuration files for development convenience, only used if --no-db is set.
	connectors := fs.String("connectors", "./static/fixtures/connectors.json.sample", "JSON file containg set of IDPC configs")
	clients := fs.String("clients", "./static/fixtures/clients.json.sample", "json file containing set of clients")
	users := fs.String("users", "./static/fixtures/users.json.sample", "json file containing set of users")

	logDebug := fs.Bool("log-debug", false, "log debug-level information")
	logTimestamps := fs.Bool("log-timestamps", false, "prefix log lines with timestamps")

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	if err := pflag.SetFlagsFromEnv(fs, "DEX_WORKER"); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	if *printVersion {
		fmt.Printf("dex version %s\ngo version %s\n", strings.TrimPrefix(version, "v"), strings.TrimPrefix(runtime.Version(), "go"))
		os.Exit(0)
	}

	if (*enableRegistration) && (*registerOnFirstLogin) {
		fmt.Fprintln(os.Stderr, "The flags --enable-registration and --enable-automatic-login cannot both be true.")
		os.Exit(1)
	}

	if *logDebug {
		log.EnableDebug()
		log.Infof("Debug logging enabled.")
		log.Debugf("Debug logging enabled.")
	}
	if *logTimestamps {
		log.EnableTimestamps()
	}

	// Validate listen address.
	lu, err := url.Parse(*listen)
	if err != nil {
		log.Fatalf("Invalid listen address %q: %v", *listen, err)
	}

	switch lu.Scheme {
	case "http":
	case "https":
		if *certFile == "" || *keyFile == "" {
			log.Fatalf("Must provide certificate file and private key file")
		}
	default:
		log.Fatalf("Only 'http' and 'https' schemes are supported")
	}

	// Validate issuer address.
	iu, err := url.Parse(*issuer)
	if err != nil {
		log.Fatalf("Invalid issuer URL %q: %v", *issuer, err)
	}

	if iu.Scheme != "http" && iu.Scheme != "https" {
		log.Fatalf("Only 'http' and 'https' schemes are supported")
	}

	if *emailFrom != "" {
		log.Errorf(`--email-from flag is depricated. Use "from" field in email config.`)
	}

	scfg := server.ServerConfig{
		IssuerURL:                    *issuer,
		TemplateDir:                  *templates,
		EmailTemplateDirs:            emailTemplateDirs,
		EmailFromAddress:             *emailFrom,
		EmailerConfigFile:            *emailConfig,
		IssuerName:                   *issuerName,
		IssuerLogoURL:                *issuerLogoURL,
		EnableRegistration:           *enableRegistration,
		EnableClientRegistration:     *enableClientRegistration,
		EnableClientCredentialAccess: *apiUseClientCredentials,
		RegisterOnFirstLogin:         *registerOnFirstLogin,
	}

	if *noDB {
		log.Warning("Running in-process without external database or key rotation")
		scfg.StateConfig = &server.SingleServerConfig{
			ClientsFile:    *clients,
			ConnectorsFile: *connectors,
			UsersFile:      *users,
		}
	} else {
		if len(keySecrets.BytesSlice()) == 0 {
			log.Fatalf("Must specify at least one key secret")
		}
		if *dbMaxIdleConns == 0 {
			log.Warning("Running with no limit on: database idle connections")
		}
		if *dbMaxOpenConns == 0 {
			log.Warning("Running with no limit on: database open connections")
		}
		dbCfg := db.Config{
			DSN:                *dbURL,
			MaxIdleConnections: *dbMaxIdleConns,
			MaxOpenConnections: *dbMaxOpenConns,
		}
		scfg.StateConfig = &server.MultiServerConfig{
			KeySecrets:     keySecrets.BytesSlice(),
			DatabaseConfig: dbCfg,
			UseOldFormat:   *useOldFormat,
		}
	}

	srv, err := scfg.Server()
	if err != nil {
		log.Fatalf("Unable to build Server: %v", err)
	}

	var cfgs []connector.ConnectorConfig
	var sleep time.Duration
	for {
		var err error
		cfgs, err = srv.ConnectorConfigRepo.All()
		if len(cfgs) > 0 && err == nil {
			break
		}
		sleep = ptime.ExpBackoff(sleep, 8*time.Second)
		if err != nil {
			log.Errorf("Unable to load connectors, retrying in %v: %v", sleep, err)
		} else {
			log.Errorf("No connectors, will wait. Retrying in %v.", sleep)
		}
		time.Sleep(sleep)
	}

	for _, cfg := range cfgs {
		cfg := cfg
		if err = srv.AddConnector(cfg); err != nil {
			log.Fatalf("Failed registering connector '%s': %v", cfg.ConnectorID(), err)
		}
	}

	h := srv.HTTPHandler()

	h = handlers.LoggingHandler(log.InfoWriter(), h)

	httpsrv := &http.Server{
		Addr:    lu.Host,
		Handler: h,
	}

	log.Infof("Binding to %s...", httpsrv.Addr)
	go func() {
		if lu.Scheme == "http" {
			log.Fatal(httpsrv.ListenAndServe())
		} else {
			log.Fatal(httpsrv.ListenAndServeTLS(*certFile, *keyFile))
		}
	}()

	<-srv.Run()
}
