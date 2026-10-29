package rtogtor

import (
	"net/url"
	"os"
	"strings"

	"github.com/TuanFaied/rtogtor/engine/rtgerr"
)

// Database products.
const (
	ProductPostgreSQL = "postgresql"
	ProductNeo4j      = "neo4j"
)

// Connection identifies a database. It holds credentials in memory only and
// never prints them: String and errors show the product, not the URL.
type Connection struct {
	product  string
	url      string
	database string
	err      error
}

// PostgreSQL returns a connection from a postgres:// or postgresql:// URL.
func PostgreSQL(connURL string) Connection {
	c := Connection{product: ProductPostgreSQL, url: connURL}
	if !strings.HasPrefix(connURL, "postgres://") && !strings.HasPrefix(connURL, "postgresql://") {
		c.err = rtgerr.New(rtgerr.ConnConfig, "a PostgreSQL connection needs a postgres:// or postgresql:// URL")
	}
	return c
}

// PostgreSQLFromEnv returns a connection from the URL in an environment
// variable.
func PostgreSQLFromEnv(name string) Connection {
	v, err := fromEnv(name)
	if err != nil {
		return Connection{product: ProductPostgreSQL, err: err}
	}
	return PostgreSQL(v)
}

// Auth holds Neo4j credentials.
type Auth struct {
	user, password string
	set            bool
	err            error
}

// NoAuth uses no credentials, or the user:password already in the URL.
func NoAuth() Auth { return Auth{} }

// BasicAuth uses a user name and password.
func BasicAuth(user, password string) Auth { return Auth{user: user, password: password, set: true} }

// BasicAuthFromEnv reads PREFIX_USER and PREFIX_PASSWORD.
func BasicAuthFromEnv(prefix string) Auth {
	user, err := fromEnv(prefix + "_USER")
	if err != nil {
		return Auth{err: err}
	}
	pw, err := fromEnv(prefix + "_PASSWORD")
	if err != nil {
		return Auth{err: err}
	}
	return BasicAuth(user, pw)
}

// Neo4j returns a connection from a neo4j://, neo4j+s://, bolt:// or
// bolt+s:// URI and credentials.
func Neo4j(uri string, auth Auth) Connection {
	c := Connection{product: ProductNeo4j}
	if auth.err != nil {
		c.err = auth.err
		return c
	}
	u, err := url.Parse(uri)
	if err != nil {
		c.err = rtgerr.New(rtgerr.ConnConfig, "invalid Neo4j URI")
		return c
	}
	if auth.set {
		u.User = url.UserPassword(auth.user, auth.password)
	}
	c.url = u.String()
	return c
}

// Neo4jFromEnv returns a connection from a URL (with user:password, if
// needed) in an environment variable.
func Neo4jFromEnv(name string) Connection {
	v, err := fromEnv(name)
	if err != nil {
		return Connection{product: ProductNeo4j, err: err}
	}
	return Neo4j(v, NoAuth())
}

// WithDatabase selects a Neo4j database other than the server default. A
// mapping's destination.database takes precedence.
func (c Connection) WithDatabase(name string) Connection {
	c.database = name
	return c
}

// Product returns the database product.
func (c Connection) Product() string { return c.product }

// IsZero reports whether the connection is unset.
func (c Connection) IsZero() bool { return c.product == "" }

// String never includes the URL or credentials.
func (c Connection) String() string {
	if c.product == "" {
		return "<no connection>"
	}
	return c.product + " connection"
}

// GoString keeps credentials out of %#v output too.
func (c Connection) GoString() string { return c.String() }

func fromEnv(name string) (string, error) {
	v, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(v) == "" {
		return "", rtgerr.New(rtgerr.ConnConfig, "environment variable %s is not set or is empty", name)
	}
	return v, nil
}
