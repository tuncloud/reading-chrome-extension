// Package notion is a thin client for the Notion endpoints the backend uses
// (spec §8), pinned to Notion-Version 2025-09-03 (C10).
package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

var defaultHTTP = &http.Client{Timeout: 15 * time.Second}

// Client calls Notion. Pacer, when set, spaces requests (worker only, C22).
type Client struct {
	BaseURL      string
	Version      string
	ClientID     string
	ClientSecret string
	HTTP         *http.Client
	Pacer        Pacer
}

// Pacer blocks until the next request may be sent.
type Pacer interface {
	Wait(ctx context.Context) error
}

// APIError is any non-2xx Notion response. Code is Notion's "code" or OAuth's "error".
type APIError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("notion: %d %s: %s", e.Status, e.Code, e.Message)
}

// AsAPIError unwraps err into *APIError.
func AsAPIError(err error) (*APIError, bool) {
	var e *APIError
	ok := errors.As(err, &e)
	return e, ok
}

type credentials struct {
	bearer string
	basic  bool
}

func bearer(token string) credentials { return credentials{bearer: token} }

var basicAuth = credentials{basic: true}

func (c *Client) do(ctx context.Context, method, path string, cred credentials, body, out any) error {
	if c.Pacer != nil {
		if err := c.Pacer.Wait(ctx); err != nil {
			return err
		}
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Notion-Version", c.Version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cred.basic {
		req.SetBasicAuth(c.ClientID, c.ClientSecret)
	} else {
		req.Header.Set("Authorization", "Bearer "+cred.bearer)
	}
	hc := c.HTTP
	if hc == nil {
		hc = defaultHTTP
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("notion: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return parseError(resp, data)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func parseError(resp *http.Response, data []byte) error {
	e := &APIError{Status: resp.StatusCode}
	var body struct {
		Code             string `json:"code"`
		Message          string `json:"message"`
		OAuthError       string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if json.Unmarshal(data, &body) == nil {
		e.Code, e.Message = body.Code, body.Message
		if e.Code == "" {
			e.Code, e.Message = body.OAuthError, body.ErrorDescription
		}
	}
	if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs >= 0 {
		e.RetryAfter = time.Duration(secs) * time.Second
	}
	return e
}
