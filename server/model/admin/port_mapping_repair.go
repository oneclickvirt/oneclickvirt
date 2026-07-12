package admin

const PortMappingRepairConfirmation = "REBUILD"

type RepairPortMappingsRequest struct {
	ProviderIDs  []uint `json:"providerIds,omitempty"`
	PortIDs      []uint `json:"portIds,omitempty"`
	DryRun       bool   `json:"dryRun,omitempty"`
	Confirmation string `json:"confirmation,omitempty"`
}

type RepairPortMappingsTaskRequest struct {
	PortIDs []uint `json:"portIds"`
}

type RepairPortMappingCandidate struct {
	PortID                  uint   `json:"portId"`
	InstanceID              uint   `json:"instanceId"`
	InstanceName            string `json:"instanceName"`
	ProviderID              uint   `json:"providerId"`
	ProviderName            string `json:"providerName"`
	HostPort                int    `json:"hostPort"`
	HostPortEnd             int    `json:"hostPortEnd"`
	GuestPort               int    `json:"guestPort"`
	GuestPortEnd            int    `json:"guestPortEnd"`
	PortCount               int    `json:"portCount"`
	Protocol                string `json:"protocol"`
	Status                  string `json:"status"`
	MappingType             string `json:"mappingType"`
	PortType                string `json:"portType"`
	RequiresInstanceRestart bool   `json:"requiresInstanceRestart"`
}

type RepairPortMappingSkipped struct {
	PortID       uint   `json:"portId"`
	InstanceID   uint   `json:"instanceId"`
	InstanceName string `json:"instanceName"`
	ProviderID   uint   `json:"providerId"`
	ProviderName string `json:"providerName"`
	HostPort     int    `json:"hostPort"`
	Reason       string `json:"reason"`
}

type RepairProviderPortMappingsPreview struct {
	ProviderID     uint                         `json:"providerId"`
	ProviderName   string                       `json:"providerName"`
	CandidateCount int                          `json:"candidateCount"`
	RuleCount      int                          `json:"ruleCount"`
	SkippedCount   int                          `json:"skippedCount"`
	Candidates     []RepairPortMappingCandidate `json:"candidates"`
	Skipped        []RepairPortMappingSkipped   `json:"skipped"`
}

type RepairPortMappingsPreviewResponse struct {
	ProviderCount                int                                 `json:"providerCount"`
	CandidateCount               int                                 `json:"candidateCount"`
	RuleCount                    int                                 `json:"ruleCount"`
	SkippedCount                 int                                 `json:"skippedCount"`
	RequiresInstanceRestartCount int                                 `json:"requiresInstanceRestartCount"`
	Providers                    []RepairProviderPortMappingsPreview `json:"providers"`
}

type RepairPortMappingsTaskFailure struct {
	ProviderID   uint   `json:"providerId"`
	ProviderName string `json:"providerName"`
	Error        string `json:"error"`
}

type RepairPortMappingsTaskResponse struct {
	Tasks       []*Task                         `json:"tasks"`
	TaskCount   int                             `json:"taskCount"`
	Failed      []RepairPortMappingsTaskFailure `json:"failed"`
	FailedCount int                             `json:"failedCount"`
}
