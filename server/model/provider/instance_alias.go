package provider

// UpdateInstanceAliasRequest permits an empty string to clear the display alias.
// A pointer distinguishes clearing from an omitted or null field.
type UpdateInstanceAliasRequest struct {
	Alias *string `json:"alias" binding:"required" example:"数据库服务器"`
}

type InstanceAliasResponse struct {
	ID    uint   `json:"id"`
	Alias string `json:"alias"`
}

// AddressPoolRequest accepts the existing addresses field for both IP families.
type AddressPoolRequest struct {
	Addresses string `json:"addresses" binding:"required" example:"192.168.1.1 - 192.168.1.100"` // 地址、CIDR或包含首尾的起始IP - 结束IP；每个显式范围最多4096个地址
}
