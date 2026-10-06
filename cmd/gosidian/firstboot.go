package main

import (
	"log"
	"log/slog"

	"github.com/gosidian/gosidian/internal/webauth"
)

// firstOwnerUsername is the owner a fresh install gets.
const firstOwnerUsername = "admin"

// provisionFirstOwner gives a fresh install its owner (IMP-044): with
// auto_owner on and no account at all, the owner "admin" with a random
// password, which the log shows once with the file that keeps it. The
// password is changed at the first sign-in, and the file goes away then.
func provisionFirstOwner(store *webauth.Store, autoOwner bool) {
	if !autoOwner {
		return
	}
	password, err := store.ProvisionInitialOwner(firstOwnerUsername)
	if err != nil {
		log.Printf("web auth: the owner of the first start was not created: %v (run `gosidian user setup`)", err)
		return
	}
	if password == "" {
		return
	}
	slog.Warn("first start: owner account created", "username", firstOwnerUsername, "password", password, "file", store.InitialPasswordPath())
	slog.Warn("first start: sign in at /login with this password; the web UI then asks you to choose your own, and the file goes away. GOSIDIAN_AUTO_OWNER=false (or [webauth] auto_owner = false) turns this off")
}
