package app

import "context"

// CredentialsEncrypted reports whether saved secrets are encrypted with a master password.
func (s *Service) CredentialsEncrypted() bool { return s.store.Encrypted() }

// Protect encrypts every saved secret with a new master password.
func (s *Service) Protect(_ context.Context, master string) error { return s.store.Protect(master) }

// ChangeMaster re-encrypts every saved secret; current must be the master password in use.
func (s *Service) ChangeMaster(_ context.Context, current, master string) error {
	return s.forgetAfter(s.store.ChangeMaster(current, master))
}

// Unprotect stores every secret in plain text again; current must be the master password in use.
func (s *Service) Unprotect(_ context.Context, current string) error {
	return s.forgetAfter(s.store.Unprotect(current))
}

// ResetCredentials removes the master password and deletes every saved secret (forgotten master password).
func (s *Service) ResetCredentials() error { return s.forgetAfter(s.store.Reset()) }

// forgetAfter drops the secrets cached by open SSH sessions once a master-password change succeeded.
func (s *Service) forgetAfter(err error) error {
	if err == nil {
		s.ssh.ForgetSecrets("")
	}
	return err
}
