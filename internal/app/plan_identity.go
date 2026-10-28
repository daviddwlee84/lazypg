package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/daviddwlee84/lazypg/internal/env"
	"github.com/pelletier/go-toml/v2"
)

// planIdentity binds review artifacts to the resolved destination and SQL inputs.
// Credentials and checkout paths are deliberately absent so CI can apply a plan.
func (a *App) planIdentity(t *env.Target, refs []DBRef) (string, string, error) {
	target := struct {
		Host                                     string
		Port                                     int
		User, SSLMode, MaintDB, Branch, Instance string
		Shared                                   bool
	}{t.Host, t.Port, t.User, t.SSLMode, t.MaintDB, t.Branch, t.Instance, t.Shared}
	raw, err := json.Marshal(target)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(raw)
	targetID := hex.EncodeToString(sum[:])
	h := sha256.New()
	config, err := toml.Marshal(a.Project)
	if err != nil {
		return "", "", err
	}
	fmt.Fprintf(h, "config:%x\n", sha256.Sum256(config))
	for _, r := range refs {
		fmt.Fprintf(h, "database:%s:%s\n", r.DB.Name, r.DBName)
		roots := []string{r.DB.Migrations, r.DB.Scripts}
		if r.DB.Bootstrap != "" {
			roots = append(roots, r.DB.Bootstrap)
		}
		for _, root := range roots {
			path := r.Owner.Abs(root)
			var files []string
			err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
				if os.IsNotExist(err) && p == path {
					return nil
				}
				if err != nil {
					return err
				}
				if !d.IsDir() {
					files = append(files, p)
				}
				return nil
			})
			if err != nil {
				return "", "", err
			}
			sort.Strings(files)
			for _, file := range files {
				data, err := os.ReadFile(file)
				if err != nil {
					return "", "", err
				}
				rel, err := filepath.Rel(path, file)
				if err != nil {
					return "", "", err
				}
				fmt.Fprintf(h, "%s:%s:%s:%x\n", r.DB.Name, root, rel, sha256.Sum256(data))
			}
		}
	}
	return targetID, hex.EncodeToString(h.Sum(nil)), nil
}
