package config

import "encoding/json"

// MarshalJSON keeps all connection fields in the file, including empty paths.
func (v savedServer) MarshalJSON() ([]byte, error) {
	type connection Server
	return json.Marshal(struct {
		connection
		KeyFile  string `json:"key_file"`
		StartDir string `json:"start_dir"`
		Secret   string `json:"secret,omitempty"`
	}{connection(v.Server), v.KeyFile, v.StartDir, v.Secret})
}
