package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/go-gorp/gorp"
	"github.com/jonboulle/clockwork"

	"github.com/coreos/dex/pkg/log"
	"github.com/coreos/dex/session"
	"github.com/coreos/go-oidc/oidc"
)

const (
	sessionTableName = "session"
)

func init() {
	register(table{
		name:    sessionTableName,
		model:   sessionModel{},
		autoinc: false,
		pkey:    []string{"id"},
	})
}

type sessionModel struct {
	ID          string `db:"id"`
	State       string `db:"state"`
	CreatedAt   int64  `db:"created_at"`
	ExpiresAt   int64  `db:"expires_at"`
	ClientID    string `db:"client_id"`
	ClientState string `db:"client_state"`
	RedirectURL string `db:"redirect_url"`
	Identity    string `db:"identity"`
	ConnectorID string `db:"connector_id"`
	UserID      string `db:"user_id"`
	Register    bool   `db:"register"`
	Nonce       string `db:"nonce"`
	Scope       string `db:"scope"`
	Groups      string `db:"groups"`
}

func (s *sessionModel) session() (*session.Session, error) {
	ru, err := url.Parse(s.RedirectURL)
	if err != nil {
		return nil, err
	}

	var ident oidc.Identity
	if err = json.Unmarshal([]byte(s.Identity), &ident); err != nil {
		return nil, err
	}
	// If this is not here, then ExpiresAt is unmarshaled with a "loc" field,
	// which breaks tests.
	if ident.ExpiresAt.IsZero() {
		ident.ExpiresAt = time.Time{}
	}

	ses := session.Session{
		ID:          s.ID,
		State:       session.SessionState(s.State),
		ClientID:    s.ClientID,
		ClientState: s.ClientState,
		RedirectURL: *ru,
		Identity:    ident,
		ConnectorID: s.ConnectorID,
		UserID:      s.UserID,
		Register:    s.Register,
		Nonce:       s.Nonce,
		Scope:       strings.Fields(s.Scope),
	}
	if s.Groups != "" {
		if err := json.Unmarshal([]byte(s.Groups), &ses.Groups); err != nil {
			return nil, fmt.Errorf("failed to decode groups in session: %v", err)
		}
	}

	if s.CreatedAt != 0 {
		ses.CreatedAt = time.Unix(s.CreatedAt, 0).UTC()
	}

	if s.ExpiresAt != 0 {
		ses.ExpiresAt = time.Unix(s.ExpiresAt, 0).UTC()
	}

	return &ses, nil
}

func newSessionModel(s *session.Session) (*sessionModel, error) {
	b, err := json.Marshal(s.Identity)
	if err != nil {
		return nil, err
	}

	sm := sessionModel{
		ID:          s.ID,
		State:       string(s.State),
		ClientID:    s.ClientID,
		ClientState: s.ClientState,
		RedirectURL: s.RedirectURL.String(),
		Identity:    string(b),
		ConnectorID: s.ConnectorID,
		UserID:      s.UserID,
		Register:    s.Register,
		Nonce:       s.Nonce,
		Scope:       strings.Join(s.Scope, " "),
	}

	if s.Groups != nil {
		data, err := json.Marshal(s.Groups)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal groups: %v", err)
		}
		sm.Groups = string(data)
	}

	if !s.CreatedAt.IsZero() {
		sm.CreatedAt = s.CreatedAt.Unix()
	}

	if !s.ExpiresAt.IsZero() {
		sm.ExpiresAt = s.ExpiresAt.Unix()
	}

	return &sm, nil
}

func NewSessionRepo(dbm *gorp.DbMap) *SessionRepo {
	return NewSessionRepoWithClock(dbm, clockwork.NewRealClock())
}

func NewSessionRepoWithClock(dbm *gorp.DbMap, clock clockwork.Clock) *SessionRepo {
	return &SessionRepo{db: &db{dbm}, clock: clock}
}

type SessionRepo struct {
	*db
	clock clockwork.Clock
}

func (r *SessionRepo) Get(sessionID string) (*session.Session, error) {
	m, err := r.executor(nil).Get(sessionModel{}, sessionID)
	if err != nil {
		return nil, err
	}

	if m == nil {
		return nil, errors.New("session does not exist")
	}

	sm, ok := m.(*sessionModel)
	if !ok {
		log.Errorf("expected sessionModel but found %v", reflect.TypeOf(m))
		return nil, errors.New("unrecognized model")
	}

	ses, err := sm.session()
	if err != nil {
		return nil, err
	}
	if ses.ExpiresAt.Before(r.clock.Now()) {
		return nil, errors.New("session does not exist")
	}

	return ses, nil
}

func (r *SessionRepo) Create(s session.Session) error {
	sm, err := newSessionModel(&s)
	if err != nil {
		return err
	}
	return r.executor(nil).Insert(sm)
}

func (r *SessionRepo) Update(s session.Session) error {
	sm, err := newSessionModel(&s)
	if err != nil {
		return err
	}
	n, err := r.executor(nil).Update(sm)
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("update affected unexpected number of rows")
	}
	return nil
}

func (r *SessionRepo) purge() error {
	qt := r.quote(sessionTableName)
	q := fmt.Sprintf("DELETE FROM %s WHERE expires_at < $1 OR state = $2", qt)
	res, err := r.executor(nil).Exec(q, r.clock.Now().Unix(), string(session.SessionStateDead))
	if err != nil {
		return err
	}

	d := "unknown # of"
	if n, err := res.RowsAffected(); err == nil {
		if n == 0 {
			return nil
		}
		d = fmt.Sprintf("%d", n)
	}

	log.Infof("Deleted %s stale row(s) from %s table", d, sessionTableName)
	return nil
}
