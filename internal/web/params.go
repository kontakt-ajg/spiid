package web

import (
	"errors"

	"spiid/internal/config"
)

type serverParams struct {
	Name     *string            `json:"name"`
	Host     *string            `json:"host"`
	Port     *int               `json:"port"`
	User     *string            `json:"user"`
	Auth     *config.AuthMethod `json:"auth"`
	KeyFile  string             `json:"key_file,omitempty"`
	StartDir string             `json:"start_dir,omitempty"`
}

func (p serverParams) server() config.Server {
	return config.Server{Name: *p.Name, Host: *p.Host, Port: *p.Port, User: *p.User,
		Auth: *p.Auth, KeyFile: p.KeyFile, StartDir: p.StartDir}
}

type settingsParams struct {
	DownloadDir    *string       `json:"download_dir"`
	ThreadsPerFile *int          `json:"threads_per_file"`
	ParallelFiles  *int          `json:"parallel_files"`
	MaxConnections *int          `json:"max_connections"`
	Theme          *config.Theme `json:"theme"`
}

func (p settingsParams) settings() config.Settings {
	return config.Settings{DownloadDir: *p.DownloadDir, ThreadsPerFile: *p.ThreadsPerFile,
		ParallelFiles: *p.ParallelFiles, MaxConnections: *p.MaxConnections, Theme: *p.Theme}
}

func checkRequired(v any) error {
	switch p := v.(type) {
	case *addParams:
		if p.Server == nil {
			return missing("server")
		}
		if err := checkRequired(p.Server); err != nil {
			return err
		}
		if p.Secret == nil {
			return missing("secret")
		}
	case *updateParams:
		if p.OldName == nil {
			return missing("old_name")
		}
		if p.Server == nil {
			return missing("server")
		}
		if err := checkRequired(p.Server); err != nil {
			return err
		}
	case *nameParams:
		if p.Name == nil {
			return missing("name")
		}
	case *masterParams:
		if p.Master == nil {
			return missing("master")
		}
	case *changeMasterParams:
		if p.Current == nil {
			return missing("current")
		}
		if p.Master == nil {
			return missing("master")
		}
	case *currentMasterParams:
		if p.Current == nil {
			return missing("current")
		}
	case *browseParams:
		if p.Server == nil {
			return missing("server")
		}
		if p.Dir == nil {
			return missing("dir")
		}
	case *downloadParams:
		if p.Server == nil {
			return missing("server")
		}
		if p.Paths == nil {
			return missing("paths")
		}
		if p.Overwrite == nil {
			return missing("overwrite")
		}
	case *idParams:
		if p.ID == nil {
			return missing("id")
		}
	case *sinceParams:
		if p.Since == nil {
			return missing("since")
		}
	case *dirParams:
		if p.Dir == nil {
			return missing("dir")
		}
	case *pathParams:
		if p.Path == nil {
			return missing("path")
		}
	case *serverParams:
		if p.Name == nil {
			return missing("name")
		}
		if p.Host == nil {
			return missing("host")
		}
		if p.Port == nil {
			return missing("port")
		}
		if p.User == nil {
			return missing("user")
		}
		if p.Auth == nil {
			return missing("auth")
		}
	case *settingsParams:
		if p.DownloadDir == nil {
			return missing("download_dir")
		}
		if p.ThreadsPerFile == nil {
			return missing("threads_per_file")
		}
		if p.ParallelFiles == nil {
			return missing("parallel_files")
		}
		if p.MaxConnections == nil {
			return missing("max_connections")
		}
		if p.Theme == nil {
			return missing("theme")
		}
	}
	return nil
}
func missing(name string) error { return invalid(errors.New("missing " + name)) }
