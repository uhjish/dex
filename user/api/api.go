package api

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/coreos/dex/client"
	clientmanager "github.com/coreos/dex/client/manager"
	"github.com/coreos/dex/pkg/log"
	"github.com/coreos/dex/refresh"
	schema "github.com/coreos/dex/schema/workerschema"
	"github.com/coreos/dex/user"
	usermanager "github.com/coreos/dex/user/manager"
)

var (
	errorMap = map[error]Error{
		user.ErrorNotFound:       ErrorResourceNotFound,
		user.ErrorDuplicateEmail: ErrorDuplicateEmail,
		user.ErrorInvalidEmail:   ErrorInvalidEmail,
		client.ErrorNotFound:     ErrorInvalidClient,
	}

	ErrorInvalidEmail  = newError("invalid_email", "invalid email.", http.StatusBadRequest)
	ErrorVerifiedEmail = newError("verified_email", "Email already verified.", http.StatusBadRequest)

	ErrorInvalidClient = newError("invalid_client", "invalid email.", http.StatusBadRequest)

	ErrorDuplicateEmail   = newError("duplicate_email", "Email already in use.", http.StatusConflict)
	ErrorResourceNotFound = newError("resource_not_found", "Resource could not be found.", http.StatusNotFound)

	ErrorUnauthorized = newError("unauthorized", "Necessary credentials not provided.", http.StatusUnauthorized)
	ErrorForbidden    = newError("forbidden", "The given user and client are not authorized to make this request.", http.StatusForbidden)

	ErrorMaxResultsTooHigh = newError("max_results_too_high", fmt.Sprintf("The max number of results per page is %d", maxUsersPerPage), http.StatusBadRequest)

	ErrorInvalidRedirectURL = newError("invalid_redirect_url", "The provided redirect URL is invalid for the given client", http.StatusBadRequest)
)

const (
	maxUsersPerPage = 100
)

func internalError(internal error) Error {
	return Error{
		Code:     http.StatusInternalServerError,
		Type:     "server_error",
		Desc:     "",
		Internal: internal,
	}
}

func newError(typ string, desc string, code int) Error {
	return Error{
		Code: code,
		Type: typ,
		Desc: desc,
	}
}

// Error is the error type returned by AdminAPI methods.
type Error struct {
	Type string

	// The HTTP Code to return for this type of error.
	Code int

	Desc string

	// The underlying error - not to be consumed by external users.
	Internal error
}

func (e Error) Error() string {
	return fmt.Sprintf("%v: Desc: %v Internal: %v", e.Type, e.Desc, e.Internal)
}

// UsersAPI is the user management API for Dex administrators.

// All calls take a Creds object with the ClientID of the calling app and the
// calling User. It is assumed that the clientID has already validated as an
// admin app before calling.
type UsersAPI struct {
	userManager      *usermanager.UserManager
	localConnectorID string
	clientManager    *clientmanager.ClientManager
	refreshRepo      refresh.RefreshTokenRepo
	emailer          Emailer
	allowClientCreds bool
}

type Emailer interface {
	SendInviteEmail(string, url.URL, string) (*url.URL, error)
}

type Creds struct {
	// IDTokens can be issued for multiple clients.
	ClientIDs []string
	User      user.User
}

// TODO(ericchiang): Don't pass a dbMap. See #385.
func NewUsersAPI(userManager *usermanager.UserManager, clientManager *clientmanager.ClientManager, refreshRepo refresh.RefreshTokenRepo, emailer Emailer, localConnectorID string, allowClientCreds bool) *UsersAPI {
	return &UsersAPI{
		userManager:      userManager,
		refreshRepo:      refreshRepo,
		clientManager:    clientManager,
		localConnectorID: localConnectorID,
		emailer:          emailer,
		allowClientCreds: allowClientCreds,
	}
}

func (u *UsersAPI) GetUser(creds Creds, id string) (schema.User, error) {
	log.Infof("userAPI: GetUser")
	if !u.Authorize(creds) {
		return schema.User{}, ErrorUnauthorized
	}

	usr, err := u.userManager.Get(id)

	if err != nil {
		return schema.User{}, mapError(err)
	}

	return userToSchemaUser(usr), nil
}

func (u *UsersAPI) DisableUser(creds Creds, userID string, disable bool) (schema.UserDisableResponse, error) {
	log.Infof("userAPI: DisableUser")
	if !u.Authorize(creds) {
		return schema.UserDisableResponse{}, ErrorUnauthorized
	}

	if err := u.userManager.Disable(userID, disable); err != nil {
		return schema.UserDisableResponse{}, mapError(err)
	}

	return schema.UserDisableResponse{
		Ok: true,
	}, nil
}

// validRedirectURL finds the first client for which the redirect URL is valid. If found it returns the client_id of the client.
func validRedirectURL(clientManager *clientmanager.ClientManager, redirectURL url.URL, clientIDs []string) (string, error) {
	// Find the first client with a valid redirectURL.
	for _, clientID := range clientIDs {
		metadata, err := clientManager.Metadata(clientID)
		if err != nil {
			return "", mapError(err)
		}

		if _, err := client.ValidRedirectURL(&redirectURL, metadata.RedirectURIs); err == nil {
			return clientID, nil
		}
	}
	return "", ErrorInvalidRedirectURL
}

func (u *UsersAPI) CreateUser(creds Creds, usr schema.User, redirURL url.URL) (schema.UserCreateResponse, error) {
	log.Infof("userAPI: CreateUser")
	if !u.Authorize(creds) {
		return schema.UserCreateResponse{}, ErrorUnauthorized
	}

	hash, err := generateTempHash()
	if err != nil {
		return schema.UserCreateResponse{}, mapError(err)
	}

	clientID, err := validRedirectURL(u.clientManager, redirURL, creds.ClientIDs)
	if err != nil {
		return schema.UserCreateResponse{}, err
	}

	id, err := u.userManager.CreateUser(schemaUserToUser(usr), user.Password(hash), u.localConnectorID)
	if err != nil {
		return schema.UserCreateResponse{}, mapError(err)
	}

	userUser, err := u.userManager.Get(id)
	if err != nil {
		return schema.UserCreateResponse{}, mapError(err)
	}

	usr = userToSchemaUser(userUser)

	url, err := u.emailer.SendInviteEmail(usr.Email, redirURL, clientID)

	// An email is sent only if we don't get a link and there's no error.
	emailSent := err == nil && url == nil

	var resetLink string
	if url != nil {
		resetLink = url.String()
	}

	return schema.UserCreateResponse{
		User:              &usr,
		EmailSent:         emailSent,
		ResetPasswordLink: resetLink,
	}, nil
}

func (u *UsersAPI) ResendEmailInvitation(creds Creds, userID string, redirURL url.URL) (schema.ResendEmailInvitationResponse, error) {
	log.Infof("userAPI: ResendEmailInvitation")
	if !u.Authorize(creds) {
		return schema.ResendEmailInvitationResponse{}, ErrorUnauthorized
	}

	clientID, err := validRedirectURL(u.clientManager, redirURL, creds.ClientIDs)
	if err != nil {
		return schema.ResendEmailInvitationResponse{}, err
	}

	// Retrieve user to check if it's already created
	userUser, err := u.userManager.Get(userID)
	if err != nil {
		return schema.ResendEmailInvitationResponse{}, mapError(err)
	}

	// Check if email is verified
	if userUser.EmailVerified {
		return schema.ResendEmailInvitationResponse{}, ErrorVerifiedEmail
	}

	url, err := u.emailer.SendInviteEmail(userUser.Email, redirURL, clientID)

	// An email is sent only if we don't get a link and there's no error.
	emailSent := err == nil && url == nil

	// If email is not sent a reset link will be generated
	var resetLink string
	if url != nil {
		resetLink = url.String()
	}

	return schema.ResendEmailInvitationResponse{
		EmailSent:         emailSent,
		ResetPasswordLink: resetLink,
	}, nil
}

func (u *UsersAPI) ListUsers(creds Creds, maxResults int, nextPageToken string) ([]*schema.User, string, error) {
	log.Infof("userAPI: ListUsers")

	if !u.Authorize(creds) {
		return nil, "", ErrorUnauthorized
	}

	if maxResults > maxUsersPerPage {
		return nil, "", ErrorMaxResultsTooHigh
	}

	users, tok, err := u.userManager.List(user.UserFilter{}, maxResults, nextPageToken)
	if err != nil {
		return nil, "", mapError(err)
	}

	list := []*schema.User{}
	for _, usr := range users {
		schemaUsr := userToSchemaUser(usr)
		list = append(list, &schemaUsr)
	}

	return list, tok, nil
}

// ListClientsWithRefreshTokens returns all clients issued refresh tokens
// for the authenticated user.
func (u *UsersAPI) ListClientsWithRefreshTokens(creds Creds, userID string) ([]*schema.RefreshClient, error) {
	// Users must either be an admin or be requesting data associated with their own account.
	if !creds.User.Admin && (creds.User.ID != userID) {
		return nil, ErrorUnauthorized
	}
	clientIdentities, err := u.refreshRepo.ClientsWithRefreshTokens(userID)
	if err != nil {
		return nil, err
	}
	clients := make([]*schema.RefreshClient, len(clientIdentities))

	urlToString := func(u *url.URL) string {
		if u == nil {
			return ""
		}
		return u.String()
	}

	for i, identity := range clientIdentities {
		clients[i] = &schema.RefreshClient{
			ClientID:   identity.Credentials.ID,
			ClientName: identity.Metadata.ClientName,
			ClientURI:  urlToString(identity.Metadata.ClientURI),
			LogoURI:    urlToString(identity.Metadata.LogoURI),
		}
	}
	return clients, nil
}

// RevokeClient revokes all refresh tokens issued to this client for the
// authenticiated user.
func (u *UsersAPI) RevokeRefreshTokensForClient(creds Creds, userID, clientID string) error {
	// Users must either be an admin or be requesting data associated with their own account.
	if !creds.User.Admin && (creds.User.ID != userID) {
		return ErrorUnauthorized
	}
	return u.refreshRepo.RevokeTokensForClient(userID, clientID)
}

func (u *UsersAPI) Authorize(creds Creds) bool {
	if u.allowClientCreds {
		if creds.User.ID == "" {
			return true
		}
	}
	return creds.User.Admin && !creds.User.Disabled
}

func userToSchemaUser(usr user.User) schema.User {
	return schema.User{
		Id:            usr.ID,
		Email:         usr.Email,
		EmailVerified: usr.EmailVerified,
		DisplayName:   usr.DisplayName,
		Admin:         usr.Admin,
		Disabled:      usr.Disabled,
		CreatedAt:     usr.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func schemaUserToUser(usr schema.User) user.User {
	return user.User{
		ID:            usr.Id,
		Email:         usr.Email,
		EmailVerified: usr.EmailVerified,
		DisplayName:   usr.DisplayName,
		Admin:         usr.Admin,
		Disabled:      usr.Disabled,
	}
}

func mapError(e error) error {
	if mapped, ok := errorMap[e]; ok {
		return mapped
	}
	return internalError(e)
}

func generateTempHash() (string, error) {
	b := make([]byte, 32)
	n, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	if n != 32 {
		return "", errors.New("unable to read enough random bytes")
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
