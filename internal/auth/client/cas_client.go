package client

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CASAttributes holds the resolved identity and group claims from a CAS response.
type CASAttributes struct {
	User     string
	Realname string
	Groups   []string
	Raw      map[string][]string
}

// CASClient describes the operations needed from a CAS 2 server.
type CASClient interface {
	LoginURL(service string) string
	LogoutURL(returnURL string) string
	ValidateTicket(ticket, service string) (CASAttributes, error)
}

// CAS client errors.
var (
	ErrCASTicketInvalid = errors.New("cas ticket invalid")
	ErrCASUnavailable   = errors.New("cas service unavailable")
)

// casClient is a small standard-library CAS 2 client.
type casClient struct {
	serverURL         string
	logoutReturnParam string
	httpClient        *http.Client
}

// NewCASClient creates a CAS client targeting the given server URL.
// logoutReturnParam is the query parameter name used on logout
// ("url" matches python-cas CAS 2 behavior; "service" matches Keycloak CAS).
func NewCASClient(serverURL, logoutReturnParam string, timeout time.Duration) CASClient {
	if logoutReturnParam == "" {
		logoutReturnParam = "url"
	}
	return &casClient{
		serverURL:         strings.TrimRight(serverURL, "/"),
		logoutReturnParam: logoutReturnParam,
		httpClient:        &http.Client{Timeout: timeout},
	}
}

func (c *casClient) LoginURL(service string) string {
	return fmt.Sprintf("%s/login?service=%s", c.serverURL, url.QueryEscape(service))
}

func (c *casClient) LogoutURL(returnURL string) string {
	return fmt.Sprintf("%s/logout?%s=%s", c.serverURL, c.logoutReturnParam, url.QueryEscape(returnURL))
}

func (c *casClient) ValidateTicket(ticket, service string) (CASAttributes, error) {
	validateURL := fmt.Sprintf(
		"%s/serviceValidate?ticket=%s&service=%s",
		c.serverURL,
		url.QueryEscape(ticket),
		url.QueryEscape(service),
	)

	resp, err := c.httpClient.Get(validateURL)
	if err != nil {
		return CASAttributes{}, fmt.Errorf("%w: %w", ErrCASUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return CASAttributes{}, fmt.Errorf("%w: cas returned status %d", ErrCASUnavailable, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return CASAttributes{}, fmt.Errorf("%w: read body: %w", ErrCASUnavailable, err)
	}

	return parseServiceResponse(body)
}

type serviceResponse struct {
	XMLName xml.Name                  `xml:"serviceResponse"`
	Success *casAuthenticationSuccess `xml:"authenticationSuccess"`
	Failure *casAuthenticationFailure `xml:"authenticationFailure"`
}

type casAuthenticationSuccess struct {
	User       string         `xml:"user"`
	Attributes *casAttributes `xml:"attributes"`
}

type casAttributes struct {
	Entries []casAttribute `xml:",any"`
}

type casAttribute struct {
	XMLName xml.Name
	Value   string `xml:",chardata"`
}

type casAuthenticationFailure struct {
	Code string `xml:"code,attr"`
	Text string `xml:",chardata"`
}

func parseServiceResponse(body []byte) (CASAttributes, error) {
	var resp serviceResponse
	if err := xml.Unmarshal(body, &resp); err != nil {
		return CASAttributes{}, fmt.Errorf("%w: parse xml: %w", ErrCASUnavailable, err)
	}

	if resp.Failure != nil {
		return CASAttributes{}, fmt.Errorf("%w: %s", ErrCASTicketInvalid, strings.TrimSpace(resp.Failure.Text))
	}
	if resp.Success == nil {
		return CASAttributes{}, fmt.Errorf("%w: no authentication result", ErrCASTicketInvalid)
	}

	attrs := parseAttributes(resp.Success.Attributes)
	if resp.Success.User != "" {
		if attrs.User == "" {
			attrs.User = strings.TrimSpace(resp.Success.User)
		}
		attrs.Raw["user"] = append(attrs.Raw["user"], strings.TrimSpace(resp.Success.User))
	}
	if attrs.User == "" {
		return CASAttributes{}, fmt.Errorf("%w: cas response missing username", ErrCASTicketInvalid)
	}
	return attrs, nil
}

func parseAttributes(attrs *casAttributes) CASAttributes {
	result := CASAttributes{Raw: make(map[string][]string)}
	if attrs == nil {
		return result
	}

	for _, entry := range attrs.Entries {
		name := entry.XMLName.Local
		value := strings.TrimSpace(entry.Value)
		if value == "" {
			continue
		}
		result.Raw[name] = append(result.Raw[name], value)

		switch name {
		case "preferred_username", "username", "eduPersonPrincipalName":
			if result.User == "" {
				result.User = value
			}
		case "name", "realname", "display_name", "displayName":
			if result.Realname == "" {
				result.Realname = value
			}
		case "groups", "group", "memberOf", "member":
			for _, g := range strings.Split(value, ",") {
				g = strings.TrimSpace(g)
				if g != "" {
					result.Groups = append(result.Groups, g)
				}
			}
		}
	}
	return result
}
