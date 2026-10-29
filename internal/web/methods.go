package web

import (
	"context"
	"encoding/json"

	"spiid/internal/config"
	"spiid/internal/errs"
)

type methodFunc func(*Server, context.Context, json.RawMessage) (any, error)

func method[P any](f func(*Server, context.Context, P) (any, error)) methodFunc {
	return func(s *Server, c context.Context, d json.RawMessage) (any, error) {
		var p P
		if e := decode(d, &p); e != nil {
			return nil, e
		}
		return f(s, c, p)
	}
}

type empty struct{}

type addParams struct {
	// Server contains the server configuration.
	Server *serverParams `json:"server"`
	// Secret is the password to save.
	Secret *string `json:"secret"`
}

type updateParams struct {
	// OldName identifies the server before this update.
	OldName *string `json:"old_name"`
	// Server contains the server configuration.
	Server *serverParams `json:"server"`
	// Secret is the password to save; nil keeps the existing secret.
	Secret *string `json:"secret"`
}

type nameParams struct {
	// Name identifies the configured server.
	Name *string `json:"name"`
}

type masterParams struct {
	Master *string `json:"master"`
}
type changeMasterParams struct {
	Current *string `json:"current"`
	Master  *string `json:"master"`
}
type currentMasterParams struct {
	Current *string `json:"current"`
}

type browseParams struct {
	// Server identifies the configured remote server.
	Server *string `json:"server"`
	// Dir selects the folder to list.
	Dir *string `json:"dir"`
}

type downloadParams struct {
	// Server identifies the configured remote server.
	Server *string `json:"server"`
	// Paths contains the remote selections to download.
	Paths *[]string `json:"paths"`
	// Overwrite permits replacing differing local files.
	Overwrite *bool `json:"overwrite"`
}

type idParams struct {
	// ID identifies the download job.
	ID *int64 `json:"id"`
}

type sinceParams struct {
	// Since is the first message index to return.
	Since *int `json:"since"`
}

var methodTable = map[string]methodFunc{
	"local.list":               method(localList),
	"local.reveal":             method(localReveal),
	"app.quit":                 method(appQuit),
	"app.info":                 method(appInfo),
	"servers.list":             method(serversList),
	"servers.add":              method(serversAdd),
	"servers.update":           method(serversUpdate),
	"servers.remove":           method(serversRemove),
	"settings.get":             method(settingsGet),
	"settings.save":            method(settingsSave),
	"credentials.reset":        method(credentialsReset),
	"credentials.status":       method(credentialsStatus),
	"credentials.protect":      method(credentialsProtect),
	"credentials.change":       method(credentialsChange),
	"credentials.unprotect":    method(credentialsUnprotect),
	"browse.list":              method(browseList),
	"downloads.add":            method(downloadsAdd),
	"downloads.state":          method(downloadsState),
	"downloads.clear_finished": method(downloadsClearFinished),
	"downloads.cancel_scan":    method(downloadsCancelScan),
	"downloads.cancel":         method(downloadsCancel),
	"downloads.retry":          method(downloadsRetry),
	"downloads.overwrite":      method(downloadsOverwrite),
}

func appQuit(_ *Server, _ context.Context, _ empty) (any, error) {
	return empty{}, nil
}

func appInfo(s *Server, _ context.Context, _ empty) (any, error) {
	return struct {
		// Version is the application version.
		Version string `json:"version"`
		// ConfigDir is the configuration folder.
		ConfigDir string `json:"config_dir"`
	}{s.service.Version(), s.service.ConfigDir()}, nil
}

func serversList(s *Server, _ context.Context, _ empty) (any, error) {
	servers := s.service.Servers()
	if servers == nil {
		servers = []config.Server{}
	}
	return struct {
		// Servers contains the configured servers.
		Servers []config.Server `json:"servers"`
	}{servers}, nil
}

func serversAdd(s *Server, c context.Context, p addParams) (any, error) {
	return empty{}, s.service.AddServer(c, p.Server.server(), *p.Secret)
}

func serversUpdate(s *Server, c context.Context, p updateParams) (any, error) {
	return empty{}, s.service.UpdateServer(c, *p.OldName, p.Server.server(), p.Secret)
}

func serversRemove(s *Server, c context.Context, p nameParams) (any, error) {
	return empty{}, s.service.RemoveServer(c, *p.Name)
}

func settingsGet(s *Server, _ context.Context, _ empty) (any, error) {
	return s.service.Settings(), nil
}

func settingsSave(s *Server, _ context.Context, p settingsParams) (any, error) {
	if e := s.service.SaveSettings(p.settings()); e != nil {
		return nil, e
	}
	return s.service.Settings(), nil
}

func credentialsStatus(s *Server, _ context.Context, _ empty) (any, error) {
	return struct {
		// Encrypted reports whether credentials use a master password.
		Encrypted bool `json:"encrypted"`
	}{s.service.CredentialsEncrypted()}, nil
}

func credentialsProtect(s *Server, c context.Context, p masterParams) (any, error) {
	return empty{}, s.service.Protect(c, *p.Master)
}

func credentialsChange(s *Server, c context.Context, p changeMasterParams) (any, error) {
	return empty{}, s.service.ChangeMaster(c, *p.Current, *p.Master)
}

func credentialsUnprotect(s *Server, c context.Context, p currentMasterParams) (any, error) {
	return empty{}, s.service.Unprotect(c, *p.Current)
}

func browseList(s *Server, c context.Context, p browseParams) (any, error) {
	l, e := s.service.List(c, *p.Server, *p.Dir)
	if e != nil {
		return nil, e
	}
	return listingJSON(l), nil
}

func downloadsAdd(s *Server, _ context.Context, p downloadParams) (any, error) {
	return empty{}, s.service.Download(*p.Server, *p.Paths, *p.Overwrite)
}

func downloadsCancel(s *Server, _ context.Context, p idParams) (any, error) {
	return empty{}, s.service.Cancel(*p.ID)
}

func downloadsRetry(s *Server, _ context.Context, p idParams) (any, error) {
	return empty{}, s.service.Retry(*p.ID)
}

func downloadsOverwrite(s *Server, _ context.Context, p idParams) (any, error) {
	return empty{}, s.service.Overwrite(*p.ID)
}

func (s *Server) callMethod(ctx context.Context, name string, params json.RawMessage) (any, error) {
	if s.ctx.Err() != nil {
		return nil, canceled()
	}
	f, ok := methodTable[name]
	if !ok {
		return nil, errs.New(errs.InvalidInput, "method", name, nil)
	}
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	c, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	return f(s, c, params)
}

func credentialsReset(s *Server, _ context.Context, _ empty) (any, error) {
	return empty{}, s.service.ResetCredentials()
}
