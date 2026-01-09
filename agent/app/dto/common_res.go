package dto

type PageResult struct {
	Total int64       `json:"total"`
	Items interface{} `json:"items"`
}

type Response struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data"`
	LogZH   string      `json:"log_zh,omitempty"`
	LogEN   string      `json:"log_en,omitempty"`
}

type Options struct {
	Option string `json:"option"`
}
