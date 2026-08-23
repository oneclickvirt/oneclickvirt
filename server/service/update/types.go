package update

import "time"

// Deployment modes are intentionally conservative. Only a verified systemd
// installation is allowed to mutate the local controller from the panel.
const (
	ModeSystemd  = "systemd"
	ModeDocker   = "docker"
	ModeCompose  = "compose"
	ModeSource   = "source"
	ModeEmbedded = "embedded"
	ModeUnknown  = "unknown"
	ModeDisabled = "disabled"
)

const (
	FlavorStandalone = "standalone"
	FlavorAllInOne   = "allinone"
)

// Command is a safe, copyable manual command. Commands are informational and
// are never executed by the server.
type Command struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Command     string `json:"command"`
	Available   bool   `json:"available"`
	Destructive bool   `json:"destructive"`
}

type Capability struct {
	Mode          string    `json:"mode"`
	Flavor        string    `json:"flavor"`
	CanUpdate     bool      `json:"canUpdate"`
	CanRollback   bool      `json:"canRollback"`
	CanRestart    bool      `json:"canRestart"`
	Automatic     bool      `json:"automatic"`
	Reason        string    `json:"reason,omitempty"`
	InstallRoot   string    `json:"installRoot,omitempty"`
	ServerPath    string    `json:"serverPath,omitempty"`
	WebPath       string    `json:"webPath,omitempty"`
	ServiceName   string    `json:"serviceName,omitempty"`
	ProxyServices []string  `json:"proxyServices,omitempty"`
	CDNEndpoints  []string  `json:"cdnEndpoints,omitempty"`
	Commands      []Command `json:"commands"`
}

type ReleaseAsset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"downloadUrl"`
	Size        int64  `json:"size,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	Digest      string `json:"digest,omitempty"`
}

type Release struct {
	Tag               string         `json:"tag"`
	Name              string         `json:"name,omitempty"`
	URL               string         `json:"url,omitempty"`
	PublishedAt       string         `json:"publishedAt,omitempty"`
	Prerelease        bool           `json:"prerelease"`
	Assets            []ReleaseAsset `json:"assets"`
	CanApply          bool           `json:"canApply"`
	CanUpdate         bool           `json:"canUpdate"`
	CanRollback       bool           `json:"canRollback"`
	UnavailableReason string         `json:"unavailableReason,omitempty"`
}

type Backup struct {
	ID        string    `json:"id"`
	Version   string    `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
	Flavor    string    `json:"flavor"`
}

type OperationState struct {
	ID         string     `json:"id"`
	Action     string     `json:"action"`
	Target     string     `json:"target,omitempty"`
	BackupID   string     `json:"backupId,omitempty"`
	Status     string     `json:"status"`
	Message    string     `json:"message,omitempty"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

const (
	OperationIdle      = "idle"
	OperationStaging   = "staging"
	OperationScheduled = "scheduled"
	OperationApplying  = "applying"
	OperationSucceeded = "succeeded"
	OperationFailed    = "failed"
)

type UpdateInfo struct {
	CurrentVersion  string         `json:"currentVersion"`
	LatestVersion   string         `json:"latestVersion,omitempty"`
	UpdateAvailable bool           `json:"updateAvailable"`
	ReleaseURL      string         `json:"releaseUrl,omitempty"`
	Error           string         `json:"error,omitempty"`
	Capability      Capability     `json:"capability"`
	Releases        []Release      `json:"releases"`
	Rollback        []Backup       `json:"rollbackVersions"`
	Operation       OperationState `json:"operation"`
	CheckedAt       time.Time      `json:"checkedAt"`
}

type UpdateRequest struct {
	Version  string `json:"version"`
	BackupID string `json:"backupId"`
}
