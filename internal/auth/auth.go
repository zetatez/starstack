// Package auth issues/validates JWT access tokens and persists refresh sessions.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/shiyi/starstack/internal/store"
)

const (
	accessTokenTTL  = 15 * time.Minute
	refreshTokenTTL = 30 * 24 * time.Hour
)

var (
	ErrInvalid  = errors.New("invalid credentials")
	ErrDisabled = errors.New("account disabled")
	ErrUnauth   = errors.New("unauthenticated")
)

// Service handles password checks and token issuance.
type Service struct {
	store  *store.Store
	secret []byte
}

func New(st *store.Store, secret string) *Service {
	return &Service{store: st, secret: []byte(secret)}
}

func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}

func ComparePassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// Login validates credentials and returns an access token + refresh token.
func (s *Service) Login(username, password string) (access, refresh string, u *store.User, err error) {
	u, err = s.store.GetUserByName(username)
	if errors.Is(err, store.ErrNotFound) {
		return "", "", nil, ErrInvalid
	}
	if err != nil {
		return "", "", nil, err
	}
	if u.Disabled {
		return "", "", nil, ErrDisabled
	}
	if !ComparePassword(u.PasswordHash, password) {
		return "", "", nil, ErrInvalid
	}
	access, refresh, err = s.Issue(u)
	return access, refresh, u, err
}

// Issue creates a fresh access + refresh token pair and stores the refresh
// token hash in the sessions table.
func (s *Service) Issue(u *store.User) (access, refresh string, err error) {
	access, err = s.signAccess(u)
	if err != nil {
		return "", "", err
	}
	refresh, err = newTokenValue()
	if err != nil {
		return "", "", err
	}
	if err := s.store.SaveSession(hashToken(refresh), u.ID, time.Now().Add(refreshTokenTTL)); err != nil {
		return "", "", err
	}
	return access, refresh, nil
}

// Refresh rotates a refresh token into a fresh pair. Old token is consumed.
func (s *Service) Refresh(refreshToken string) (access, newRefresh string, u *store.User, err error) {
	h := hashToken(refreshToken)
	userID, exp, err := s.store.GetSession(h)
	if err != nil {
		return "", "", nil, ErrUnauth
	}
	if time.Now().After(exp) {
		_ = s.store.DeleteSession(h)
		return "", "", nil, ErrUnauth
	}
	u, err = s.store.GetUserByID(userID)
	if err != nil {
		return "", "", nil, ErrUnauth
	}
	if u.Disabled {
		return "", "", nil, ErrDisabled
	}
	newRefresh, err = newTokenValue()
	if err != nil {
		return "", "", nil, err
	}
	if err := s.store.RotateSession(h, hashToken(newRefresh), u.ID, time.Now().Add(refreshTokenTTL)); err != nil {
		return "", "", nil, err
	}
	access, err = s.signAccess(u)
	if err != nil {
		return "", "", nil, err
	}
	return access, newRefresh, u, nil
}

func (s *Service) signAccess(u *store.User) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":  u.ID,
		"name": u.Username,
		"iat":  now.Unix(),
		"exp":  now.Add(accessTokenTTL).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
}

// ParseAccessToken verifies signature/expiry and returns the user.
func (s *Service) ParseAccessToken(tokenStr string) (*store.User, error) {
	tok, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil || !tok.Valid {
		return nil, ErrUnauth
	}
	claims, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return nil, ErrUnauth
	}
	sub, ok := claims["sub"].(float64)
	if !ok {
		return nil, ErrUnauth
	}
	return s.store.GetUserByID(int64(sub))
}

func (s *Service) Logout(refreshToken string) error {
	return s.store.DeleteSession(hashToken(refreshToken))
}

func newTokenValue() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}
