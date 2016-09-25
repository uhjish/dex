# Getting Started


# Introduction

In this document we'll stand up the full dex stack on a single machine. This should demonstrate all the moving parts involved in a dex installation, but is not appropriate for production deployment. Please see the [deployment guide][deployment-guide] for information on production dex setups.

[deployment-guide]: https://github.com/coreos/dex/blob/master/Documentation/deploy-guide.md

We'll also start the example web app, so we can try registering and logging in.

# Pre-requisites

Before continuing, you must have the following installed on your system:

* Go 1.4 or greater
* Postgres 9.4 or greater (this guide also assumes that Postgres is up and running)

In addition, if you wish to try out authenticating against Google's OIDC backend, you must have a new client registered with Google:

* Go to https://console.developers.google.com/project and select an existing project or create a new project.
* Click on APIs and auth > Credentials, and select an OAuth 2 client ID from the Add credentials dropdown.
* On the "Create Client ID" screen, choose "Web Application", provide a Name and enter `http://127.0.0.1:5556/dex/auth/google/callback` for your Authorised redirect URI.
* The generated client ID and client secret will be needed later.

# Create Database

On the PostgreSQL server, login as a user with appropriate permissions and create a database and user for dex to use. These can be named arbitrarily, but are called `dex_db` and `dex`, respectively, in this example.

```sql
CREATE DATABASE dex_db;
CREATE USER dex WITH PASSWORD 'dex_pass';
GRANT ALL PRIVILEGES ON DATABASE dex_db TO dex;
```

Store the [connection string](http://www.postgresql.org/docs/9.4/static/libpq-connect.html#LIBPQ-CONNSTRING) for the dex database in an environment variable:

```
DEX_DB_URL=postgres://dex:dex_pass@localhost/dex_db?sslmode=disable
```

# Building

The build script will build all dex components.

`./build`

# Generate a Secret Symmetric Key

dex needs a 32 byte base64-encoded key which will be used to encrypt the private keys in the database. A good way to generate the key is to read from /dev/random:

`DEX_KEY_SECRET=$(dd if=/dev/random bs=1 count=32 2>/dev/null | base64 | tr -d '\n')`

The dex overlord and workers allow multiple key secrets (separated by commas) to be passed but only the first will be used to encrypt data; the rest are there for decryption only; this scheme allows for the rotation of keys without downtime (assuming a rolling restart of workers).

# Generate an Admin API Secret

The dex overlord has an API which is very powerful - you can create Admin users with it, so it needs to be protected somehow. This is accomplished by requiring that a secret is passed via the Authorization header of each request. This secret is 128 bytes base64 encoded, and should be sufficiently random so as to make guessing impractical:

`DEX_OVERLORD_ADMIN_API_SECRET=$(dd if=/dev/random bs=1 count=128 2>/dev/null | base64 | tr -d '\n')`

# Start the overlord

The overlord is responsible for creating and rotating keys and some other administrative tasks. In addition, the overlord is responsible for creating the necessary database tables (and when you update, performing schema migrations), so it must be started before we do anything else. Debug logging is turned on so we can see more of what's going on. Start it up.

`./bin/dex-overlord --admin-api-secret=$DEX_OVERLORD_ADMIN_API_SECRET --db-url=$DEX_DB_URL --key-secrets=$DEX_KEY_SECRET --log-debug=true &`

## Environment Variables.

Note that parameters can be passed as flags or environment variables to dex components; an equivalent start with environment variables would be:

```
export DEX_OVERLORD_ADMIN_API_SECRET=$DEX_OVERLORD_ADMIN_API_SECRET
export DEX_OVERLORD_DB_URL=$DEX_DB_URL
export DEX_OVERLORD_KEY_SECRETS=$DEX_KEY_SECRET
export DEX_OVERLORD_LOG_DEBUG=true
./bin/dex-overlord &
```

# Start the dex-worker

Before starting `dex-worker` you should determine how you want verification emails to be delivered to the user.
If you just want to test dex out, you can just use the provided sample config in `static/fixtures/emailer.json.sample`.
Please review [email-configuration](https://github.com/coreos/dex/blob/master/Documentation/email-configuration.md) for details
(make sure you point `--email-cfg` to your newly configured file).

Once you have setup your email config run `dex-worker`:

`./bin/dex-worker --db-url=$DEX_DB_URL --key-secrets=$DEX_KEY_SECRET --email-cfg=static/fixtures/emailer.json.sample --enable-registration=true --log-debug=true &`

Now you have a worker which you can authenticate against, listening on `http://0.0.0.0:5556`, which is the default. Note that the default issuer URL (which can be changed on --issuer) is `http://127.0.0.1:5556`. The issuer URL is the base URL (i.e. no query or fragments) uniquely identifying your dex installation.

Note: the issuer URL MUST have an `https` scheme in production to meet spec compliance and to be considered reasonably secure.

# Set up Connectors

The worker and overlord are up and running, but we need to tell dex what connectors we want to use to authenticate. For this case we'll set up a local connector, where dex manages credentials and provides a UI for authentication, and a Google OIDC connector.

If you prefer to use the Google OIDC Identity Provider (IdP), just omit the second entry in the JSON connector list. Note that you must replace `DEX_GOOGLE_CLIENT_SECRET` and `DEX_GOOGLE_CLIENT_ID` with the client secret and client ID you got when you registered your project with the Google developer console.

```
cat << EOF > /tmp/dex_connectors.json
[
	{
		"type": "local",
		"id": "local"
	},
	{
		"type": "oidc",
		"id": "google",
		"issuerURL": "https://accounts.google.com",
		"clientID": "$DEX_GOOGLE_CLIENT_ID",
		"clientSecret": "$DEX_GOOGLE_CLIENT_SECRET",
		"trustedEmailProvider": true
	}
]
EOF
./bin/dexctl --db-url=$DEX_DB_URL set-connector-configs /tmp/dex_connectors.json
```

One thing to note here that's a bit confusing here is that in the case of the Google OIDC connector, dex is the client and Google is the IdP, but when you're dealing with your own apps that want to authenticate against dex, your app is the client and dex is the IdP.

# Register a Client

Like all OAuth2/OIDC IdPs, clients must be registered with the IdP (dex), along with their valid redirect URLS.

New clients can be registered with the dexctl CLI tool:

```
eval "$(./bin/dexctl --db-url=$DEX_DB_URL new-client http://127.0.0.1:5555/callback)"
```

The output of this command is eval'able if you are in bash, and sets the following shell variables:

```
DEX_APP_CLIENT_ID
DEX_APP_CLIENT_SECRET
```

# Start the Example Web App

The included example app demonstrates registering and authenticating with dex. Start it up:

```
./bin/example-app --client-id=$DEX_APP_CLIENT_ID --client-secret=$DEX_APP_CLIENT_SECRET --discovery=http://127.0.0.1:5556/dex &
```

# Authenticate with dex!

Go to `127.0.0.1:5555`, and click "register"; choose either "Google", if you have a Google Account and would like to use that to authenticate. Otherwise, choose "local".

If you chose Google, enter your credentials (if you are not logged into Google) and click through the authorization screen. If you chose "local", enter a name and password and submit.

After registering you should end up back at the example app, where it will display the claims returned by dex.

# Verify Your Email

If you registered with Google, your email address is already verified, and this should be reflected by the presence of an `email_verified` claim. Otherwise, you need to verify your email address.

In a fully configured production environment an email provider will be set up so that dex can email users email verification links (amongst other things); in this setup, we are using the `FakeEmailer` email provider which simply outputs to stdout. Look for the "Welcome to Dex!" message in your console and copy the link that follows it, and then paste it in your browser; you should end up back at the example app page that displays claims, but this time you'll see a tru `email_verified` claim.

# Standup Dev Script

A script which does almost everything in this guide exists at `contrib/standup-db.sh`. Read the comments inside before attempting to run it - it requires a little setup beforehand.
