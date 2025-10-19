package common

type Response struct {
	Code int         `json:"code"`
	Data interface{} `json:"data"`
	Msg  string      `json:"msg"`
}

type PageResult struct {
	List     interface{} `json:"list"`
	Total    int64       `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"pageSize"`
}

func Success(data interface{}) map[string]interface{} {
	return map[string]interface{}{
		"code": 0,
		"data": data,
		"msg":  "success",
	}
}

func Error(msg string) map[string]interface{} {
	return map[string]interface{}{
		"code": 7,
		"data": nil,
		"msg":  msg,
	}
}
