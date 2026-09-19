package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestInstallerWritesLiteralDatabaseConfiguration(t *testing.T) {
	script, err := filepath.Abs("../../scripts/install_full.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"false", "true"} {
		t.Run("external-"+mode, func(t *testing.T) {
			directory := t.TempDir()
			password := "  literal@:/?&%'\"\\pass\n\t\b\f  "
			command := exec.Command("bash", "-c", `source "$OCV_INSTALL_SCRIPT"; SERVER_DIR="$OCV_CONFIG_DIR"; EXTERNAL_DB="$OCV_EXTERNAL"; DB_PASSWORD="$OCV_PASSWORD"; DB_PASS_EXT="$OCV_PASSWORD"; DB_HOST='2001:db8::1'; DB_USER_EXT='user: name'; DB_NAME_EXT='database # literal'; mkdir -p "$SERVER_DIR"; write_application_config`)
			command.Env = append(os.Environ(), "OCV_CONFIG_DIR="+directory, "OCV_EXTERNAL="+mode, "OCV_PASSWORD="+password)
			command.Env = append(command.Env, "OCV_INSTALL_SCRIPT="+script)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("installer config failed: %v, %s", err, output)
			}
			path := filepath.Join(directory, "config.yaml")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var config struct {
				Mysql struct {
					Password, Path, Username string
					Database                 string `yaml:"db-name"`
				}
			}
			if err := yaml.Unmarshal(data, &config); err != nil {
				t.Fatal("installer wrote invalid YAML")
			}
			if config.Mysql.Password != password {
				t.Fatal("installer changed password bytes")
			}
			if mode == "true" && (config.Mysql.Path != "2001:db8::1" || config.Mysql.Username != "user: name" || config.Mysql.Database != "database # literal") {
				t.Fatal("installer changed external endpoint/account")
			}
			stat, err := os.Stat(path)
			if err != nil || stat.Mode().Perm() != 0600 {
				t.Fatal("database credentials not private")
			}
		})
	}
}

func TestInstallerNoninteractiveEnvironment(t *testing.T) {
	script, err := filepath.Abs("../../scripts/install_full.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ lower, upper, expected string }{{"true", "false", "true"}, {"false", "true", "false"}, {"", "true", "true"}} {
		cmd := exec.Command("bash", "-c", `source "$OCV_INSTALL_SCRIPT"; printf '%s' "$NONINTERACTIVE"`)
		cmd.Env = append(os.Environ(), "noninteractive="+tc.lower, "NONINTERACTIVE="+tc.upper)
		cmd.Env = append(cmd.Env, "OCV_INSTALL_SCRIPT="+script)
		output, err := cmd.CombinedOutput()
		if err != nil || string(output) != tc.expected {
			t.Fatalf("environment mode lost: got %q want %q: %v", output, tc.expected, err)
		}
	}
}
