package constant

const (
	// ProviderDefaultConcurrentTasks is the safe fallback for Provider task workers.
	ProviderDefaultConcurrentTasks = 1
	// ProviderMaxConcurrentTasks matches the frontend limit and protects API/CSV paths.
	ProviderMaxConcurrentTasks = 10
)
