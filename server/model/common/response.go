package common

// Response 统一响应格式（Swagger文档使用）
type Response struct {
	Code    int         `json:"code"`
	Data    interface{} `json:"data"`
	Msg     string      `json:"msg"`
	Message string      `json:"message"`
}

// PageResult 分页响应格式（Swagger文档使用）
type PageResult struct {
	List     interface{} `json:"list"`
	Total    int64       `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"pageSize"`
}
