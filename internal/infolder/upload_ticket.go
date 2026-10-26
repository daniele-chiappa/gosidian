package infolder

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
)

// The ticket of an upload opened through MCP (IMP-172 phase 5b): the
// credential an agent sends its pieces with, from a shell that has no
// bearer of its own. It lasts as long as the upload; in.db keeps only its
// SHA-256 and the token that minted it, so that a restart of the server
// does not cut an agent off, and a new one replaces it.

// MintUploadTicket gives an upload of an account a new ticket, for the
// token tokenID, and returns it: the one before stops working.
func (s *Service) MintUploadTicket(project, id, ownerID, tokenID string) (string, error) {
	if _, err := s.owned(project, id, ownerID); err != nil {
		return "", err
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	ticket := hex.EncodeToString(b[:])
	if err := s.store.setUploadTicket(id, hashTicket(ticket), tokenID); err != nil {
		return "", err
	}
	return ticket, nil
}

// UploadByTicket returns the upload a ticket opens, without its files, and
// the token that minted the ticket; ErrUploadNotFound for any other.
func (s *Service) UploadByTicket(id, ticket string) (Upload, string, error) {
	hash, tokenID, err := s.store.uploadTicket(id)
	if err != nil {
		return Upload{}, "", err
	}
	if hash == "" || ticket == "" || subtle.ConstantTimeCompare([]byte(hash), []byte(hashTicket(ticket))) != 1 {
		return Upload{}, "", ErrUploadNotFound
	}
	u, err := s.store.upload(id, false)
	if err != nil {
		return Upload{}, "", err
	}
	s.fill(&u)
	return u, tokenID, nil
}

func hashTicket(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}
