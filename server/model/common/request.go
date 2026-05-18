package common

const DefaultPageSize = 10

type PageInfo struct {
	Page     int    `json:"page" form:"page"`
	PageSize int    `json:"pageSize" form:"pageSize"`
	Keyword  string `json:"keyword" form:"keyword"`
}

func NormalizePagination(page, pageSize, defaultPageSize int) (int, int) {
	if defaultPageSize <= 0 {
		defaultPageSize = DefaultPageSize
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	return page, pageSize
}

func (p *PageInfo) Normalize(defaultPageSize int) {
	p.Page, p.PageSize = NormalizePagination(p.Page, p.PageSize, defaultPageSize)
}
