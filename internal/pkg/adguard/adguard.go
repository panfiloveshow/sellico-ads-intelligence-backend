// Package adguard defines the shared native advertising transport contract.
// It records attempts, not successful vendor state changes. Observations are
// a separate step; no request body contains a credential in this journal.
package adguard

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

type Request struct {
	Marketplace, AccountKey, Method, URL, Fingerprint string
	Resources                                         []string
}
type Lease struct{ ID uuid.UUID }
type Outcome struct {
	State        string
	HTTPStatus   int
	ResponseHash string
}
type Guard interface {
	BeginAdvertisingMutation(context.Context, Request) (Lease, error)
	CompleteAdvertisingMutation(context.Context, Lease, Outcome) error
}
type commandKey struct{}
type approvalGateKey struct{}
type ApprovalGate func(context.Context, Request) error

// Only server code can attach this request-local gate. It runs after the
// native client has acquired its token/limiter and while the durable hold exists.
func WithApprovedCommandGate(ctx context.Context, id uuid.UUID, gate ApprovalGate) context.Context {
	return context.WithValue(WithApprovedCommand(ctx, id), approvalGateKey{}, gate)
}
func CheckApprovalGate(ctx context.Context, r Request) error {
	gate, _ := ctx.Value(approvalGateKey{}).(ApprovalGate)
	if gate == nil {
		return errors.New("fresh personal advertising approval gate unavailable")
	}
	return gate(ctx, r)
}

func WithApprovedCommand(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, commandKey{}, id)
}
func ApprovedCommand(ctx context.Context) uuid.UUID {
	v, _ := ctx.Value(commandKey{}).(uuid.UUID)
	return v
}

// MutationError preserves uncertainty through legacy service error wrappers.
type MutationError struct {
	Err            error
	OutcomeUnknown bool
	NotSent        bool
}

func (e *MutationError) Error() string { return e.Err.Error() }
func (e *MutationError) Unwrap() error { return e.Err }
func IsUnknown(err error) bool         { var v *MutationError; return errors.As(err, &v) && v.OutcomeUnknown }
func WasNotSent(err error) bool        { var v *MutationError; return errors.As(err, &v) && v.NotSent }
func BeforeSendError(err error) error {
	if err == nil {
		return nil
	}
	return &MutationError{Err: err, NotSent: true}
}
func AfterSendError(err error, status int) error {
	if err == nil {
		return nil
	}
	return &MutationError{Err: err, OutcomeUnknown: status == 0 || status >= 500 || status == 408 || status == 409 || status < 400}
}
func Result(status int, body []byte, err error) Outcome {
	state := "unknown"
	if err == nil && status >= 200 && status < 300 {
		state = "acknowledged"
	} else if status >= 400 && status < 500 && status != 408 && status != 409 {
		state = "rejected"
	}
	sum := sha256.Sum256(body)
	return Outcome{State: state, HTTPStatus: status, ResponseHash: hex.EncodeToString(sum[:])}
}

func IsWBMutation(method, path string) bool {
	u, err := url.Parse(path)
	if err != nil {
		return true
	}
	if method == http.MethodGet {
		switch u.Path {
		case "/adv/v0/start", "/adv/v0/pause", "/adv/v0/stop", "/adv/v0/delete":
			return true
		}
		return false
	}
	if method == http.MethodHead || method == http.MethodOptions {
		return false
	}
	if method == http.MethodPost {
		switch u.Path {
		case "/api/advert/v2/budget", "/api/advert/v1/bids/min", "/adv/v1/promotion/adverts", "/adv/v0/normquery/get-minus", "/adv/v0/normquery/list", "/adv/v1/normquery/stats", "/adv/v0/normquery/get-bids", "/adv/v2/fullstats", "/adv/v3/fullstats":
			return false
		}
	}
	return true
}
func IsOzonMutation(method, path string) bool {
	u, err := url.Parse(path)
	if err != nil {
		return true
	}
	if method == http.MethodGet {
		switch u.Path {
		case "/api/client/campaign/all_sku_promo/activate", "/api/client/campaign/all_sku_promo/set_bid", "/api/client/campaign/all_sku_promo/deactivate":
			return true
		}
		return false
	}
	if method == http.MethodHead || method == http.MethodOptions {
		return false
	}
	if method == http.MethodPost {
		switch u.Path {
		case "/api/client/statistics", "/api/client/statistics/phrases", "/api/client/min/sku", "/api/client/campaign/search_promo/v2/products", "/api/client/search_promo/get_cpo_min_bids":
			return false
		}
	}
	return true
}

var positiveID = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
var sellerID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

func AccountKey(marketplace, identity string) string {
	s := sha256.Sum256([]byte(marketplace + "\x00" + identity))
	return marketplace + ":" + hex.EncodeToString(s[:])
}
func WBAccountKey(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(parts[1]) > 32768 {
		return "", errors.New("WB token does not establish a stable seller identity")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("WB seller identity is unreadable")
	}
	var p struct {
		Seller string `json:"sid"`
	}
	if json.Unmarshal(b, &p) != nil || !sellerID.MatchString(p.Seller) {
		return "", errors.New("WB token does not establish a stable seller identity")
	}
	// The claim supplies an identity key, not local authentication. WB still
	// authenticates the original token; modifying the claim invalidates it.
	return AccountKey("wb", p.Seller), nil
}

func NewWBRequest(method, rawURL, token string, body []byte) (Request, error) {
	key, err := WBAccountKey(token)
	if err != nil {
		return Request{}, err
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return Request{}, err
	}
	ids := []string{}
	if v := u.Query().Get("id"); v != "" {
		if !positiveID.MatchString(v) {
			return Request{}, errors.New("invalid WB campaign identity")
		}
		ids = append(ids, v)
	}
	if len(body) > 0 {
		var tree any
		d := json.NewDecoder(bytes.NewReader(body))
		d.UseNumber()
		if d.Decode(&tree) != nil {
			return Request{}, errors.New("invalid WB advertising payload")
		}
		var walk func(any) error
		walk = func(v any) error {
			switch x := v.(type) {
			case []any:
				for _, item := range x {
					if err := walk(item); err != nil {
						return err
					}
				}
			case map[string]any:
				for k, item := range x {
					if u.Path == "/api/advert/v0/daily-limits" && k == "advertIds" {
						list, ok := item.([]any)
						if !ok || len(list) == 0 || len(list) > 100 {
							return errors.New("specific WB daily-limit campaign IDs required")
						}
						for _, value := range list {
							number, ok := value.(json.Number)
							if !ok || !positiveID.MatchString(number.String()) {
								return errors.New("integer WB daily-limit campaign identity required")
							}
							id, err := strconv.ParseInt(number.String(), 10, 64)
							if err != nil || id <= 0 {
								return errors.New("WB daily-limit campaign ID outside native range")
							}
							ids = append(ids, number.String())
						}
						continue
					}
					if k == "advert_id" || k == "advertId" || (u.Path == "/adv/v1/budget/deposit" && k == "id") {
						var id string
						switch n := item.(type) {
						case json.Number:
							id = n.String()
						case string:
							id = n
						}
						if !positiveID.MatchString(id) {
							return errors.New("invalid WB campaign identity")
						}
						ids = append(ids, id)
					} else if err := walk(item); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if err := walk(tree); err != nil {
			return Request{}, err
		}
	}
	return NewRequest("wb", key, method, rawURL, ids, body)
}
func NewOzonRequest(method, rawURL, clientID string, body []byte) (Request, error) {
	if clientID == "" || len(clientID) > 500 {
		return Request{}, errors.New("Ozon performance account identity unavailable")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return Request{}, err
	}
	ids := []string{}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) >= 4 && parts[0] == "api" && parts[1] == "client" && parts[2] == "campaign" && positiveID.MatchString(parts[3]) {
		ids = append(ids, parts[3])
	}
	return NewRequest("ozon", AccountKey("ozon", clientID), method, rawURL, ids, body)
}
func NewRequest(marketplace, account, method, rawURL string, resources []string, body []byte) (Request, error) {
	if (marketplace != "wb" && marketplace != "ozon") || !strings.HasPrefix(account, marketplace+":") || len(account) != len(marketplace)+65 || len(body) > 8*1024*1024 {
		return Request{}, errors.New("unbounded advertising mutation")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.User != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Fragment != "" {
		return Request{}, errors.New("unverified advertising endpoint")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return Request{}, err
	}
	u.RawQuery = query.Encode()
	set := map[string]bool{}
	for _, id := range resources {
		if id != "*" && !positiveID.MatchString(id) {
			return Request{}, errors.New("invalid advertising campaign ID")
		}
		set[id] = true
	}
	ids := []string{}
	for id := range set {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		ids = []string{"*"}
	}
	if len(ids) > 1000 || (len(ids) > 1 && set["*"]) {
		return Request{}, errors.New("unbounded advertising resources")
	}
	sort.Strings(ids)
	canonical := body
	if len(bytes.TrimSpace(body)) > 0 {
		var v any
		d := json.NewDecoder(bytes.NewReader(body))
		d.UseNumber()
		if d.Decode(&v) != nil || d.Decode(&struct{}{}) != io.EOF {
			return Request{}, errors.New("invalid advertising request JSON")
		}
		canonical, err = json.Marshal(v)
		if err != nil {
			return Request{}, err
		}
	}
	identity, _ := json.Marshal([]any{"ad_native_request_v1", marketplace, account, method, u.String(), ids, string(canonical)})
	hash := sha256.Sum256(identity)
	return Request{Marketplace: marketplace, AccountKey: account, Method: method, URL: u.String(), Fingerprint: hex.EncodeToString(hash[:]), Resources: ids}, nil
}
func (r Request) ResourceKeys() []string {
	keys := make([]string, 0, len(r.Resources))
	for _, id := range r.Resources {
		if id == "*" {
			keys = append(keys, r.AccountKey+":*")
		} else {
			keys = append(keys, r.Marketplace+":campaign:"+id)
		}
	}
	return keys
}
func (r Request) Validate() error {
	if (r.Marketplace != "wb" && r.Marketplace != "ozon") || !strings.HasPrefix(r.AccountKey, r.Marketplace+":") || len(r.Fingerprint) != 64 || len(r.Resources) == 0 || len(r.Resources) > 1000 {
		return fmt.Errorf("advertising request identity missing")
	}
	for _, v := range []string{r.Fingerprint, strings.TrimPrefix(r.AccountKey, r.Marketplace+":")} {
		b, err := hex.DecodeString(v)
		if err != nil || len(b) != 32 {
			return errors.New("invalid advertising request digest")
		}
	}
	seen := map[string]bool{}
	for _, id := range r.Resources {
		if seen[id] || (id == "*" && len(r.Resources) != 1) || (id != "*" && !positiveID.MatchString(id)) {
			return errors.New("invalid advertising resource set")
		}
		seen[id] = true
		if id != "*" {
			if _, err := strconv.ParseInt(id, 10, 64); err != nil {
				return errors.New("advertising campaign outside int64 range")
			}
		}
	}
	if !sort.StringsAreSorted(r.Resources) {
		return errors.New("advertising resources are not canonical")
	}
	return nil
}
