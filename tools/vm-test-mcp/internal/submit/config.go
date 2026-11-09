package submit

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"

	"github.com/goccy/go-yaml"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type UsageError struct{ Message string }

func (e *UsageError) Error() string { return e.Message }
func usage(message string) error    { return &UsageError{Message: message} }
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var argument *UsageError
	if errors.As(err, &argument) {
		return 2
	}
	return 1
}

// Connection contains only the submission client's connection credentials.
type Connection struct {
	Endpoint string `yaml:"endpoint"`
	Token    string `yaml:"token"`
}

func LoadConnection(file string) (Connection, error) {
	var c Connection
	f, err := os.Open(file)
	if err != nil {
		return c, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, wire.MaxJSON+1))
	if err != nil || len(data) > wire.MaxJSON {
		return c, fmt.Errorf("cannot read client configuration within its byte limit")
	}
	d := yaml.NewDecoder(bytes.NewReader(data), yaml.Strict())
	if err = d.Decode(&c); err != nil {
		return Connection{}, fmt.Errorf("client YAML requires only endpoint and token fields")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return c, fmt.Errorf("client configuration requires one YAML document")
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/mcp" || c.Token == "" {
		return c, fmt.Errorf("client configuration requires an absolute HTTP(S) /mcp endpoint and token")
	}
	return c, nil
}
