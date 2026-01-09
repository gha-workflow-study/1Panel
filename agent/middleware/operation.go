package middleware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path"
	"reflect"
	"strings"
	"time"

	"github.com/1Panel-dev/1Panel/agent/cmd/server/docs"
	"github.com/1Panel-dev/1Panel/agent/global"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func OperationLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.Contains(c.Request.URL.Path, "search") || c.Request.Method == http.MethodGet {
			c.Next()
			return
		}
		pathItem := strings.TrimPrefix(c.Request.URL.Path, "/api/v2")
		pathItem = strings.TrimPrefix(pathItem, "/api/v2/core")
		swagger := make(map[string]operationJson)
		if err := json.Unmarshal(docs.XLogJson, &swagger); err != nil {
			c.Next()
			return
		}
		operationDic, hasPath := swagger[pathItem]
		if !hasPath {
			c.Next()
			return
		}
		if len(operationDic.FormatZH) == 0 {
			c.Next()
			return
		}

		formatMap := make(map[string]interface{})
		if len(operationDic.BodyKeys) != 0 {
			body, err := io.ReadAll(c.Request.Body)
			if err == nil {
				c.Request.Body = io.NopCloser(bytes.NewBuffer(body))
			}
			bodyMap := make(map[string]interface{})
			if strings.Contains(c.Request.Header.Get("Content-Type"), "multipart/form-data") {
				bodyMap, _ = parseMultipart(body, c.Request.Header.Get("Content-Type"))
			} else {
				_ = json.Unmarshal(body, &bodyMap)
			}
			for _, key := range operationDic.BodyKeys {
				if _, ok := bodyMap[key]; ok {
					formatMap[key] = bodyMap[key]
				}
			}
		}
		if len(operationDic.BeforeFunctions) != 0 {
			dbItem, err := newDB(pathItem)
			if err != nil {
				c.Next()
				return
			}
			for _, funcs := range operationDic.BeforeFunctions {
				for key, value := range formatMap {
					if funcs.InputValue == key {
						var names []string
						if funcs.IsList {
							sql := fmt.Sprintf("SELECT %s FROM %s where %s in (?);", funcs.OutputColumn, funcs.DB, funcs.InputColumn)
							_ = dbItem.Raw(sql, value).Scan(&names)
						} else {
							_ = dbItem.Raw(fmt.Sprintf("select %s from %s where %s = ?;", funcs.OutputColumn, funcs.DB, funcs.InputColumn), value).Scan(&names)
						}
						formatMap[funcs.OutputValue] = strings.Join(names, ",")
						break
					}
				}
			}
			closeDB(dbItem)
		}
		for key, value := range formatMap {
			if strings.Contains(operationDic.FormatEN, "["+key+"]") {
				t := reflect.TypeOf(value)
				if t.Kind() != reflect.Array && t.Kind() != reflect.Slice {
					operationDic.FormatZH = strings.ReplaceAll(operationDic.FormatZH, "["+key+"]", fmt.Sprintf("[%v]", value))
					operationDic.FormatEN = strings.ReplaceAll(operationDic.FormatEN, "["+key+"]", fmt.Sprintf("[%v]", value))
				} else {
					val := reflect.ValueOf(value)
					length := val.Len()

					var elements []string
					for i := 0; i < length; i++ {
						element := val.Index(i).Interface().(string)
						elements = append(elements, element)
					}
					operationDic.FormatZH = strings.ReplaceAll(operationDic.FormatZH, "["+key+"]", fmt.Sprintf("[%v]", strings.Join(elements, ",")))
					operationDic.FormatEN = strings.ReplaceAll(operationDic.FormatEN, "["+key+"]", fmt.Sprintf("[%v]", strings.Join(elements, ",")))
				}
			}
		}
		c.Set("1panel_zh_log", strings.ReplaceAll(operationDic.FormatZH, "[]", ""))
		c.Set("1panel_en_log", strings.ReplaceAll(operationDic.FormatEN, "[]", ""))
		c.Next()
	}
}

type operationJson struct {
	API             string         `json:"api"`
	Method          string         `json:"method"`
	BodyKeys        []string       `json:"bodyKeys"`
	ParamKeys       []string       `json:"paramKeys"`
	BeforeFunctions []functionInfo `json:"beforeFunctions"`
	FormatZH        string         `json:"formatZH"`
	FormatEN        string         `json:"formatEN"`
}
type functionInfo struct {
	InputColumn  string `json:"input_column"`
	InputValue   string `json:"input_value"`
	IsList       bool   `json:"isList"`
	DB           string `json:"db"`
	OutputColumn string `json:"output_column"`
	OutputValue  string `json:"output_value"`
}

type responseBodyWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (r responseBodyWriter) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

func newDB(pathItem string) (*gorm.DB, error) {
	dbFile := ""
	switch {
	case strings.HasPrefix(pathItem, "/core"):
		dbFile = path.Join(global.CONF.Base.InstallDir, "1panel/db/core.db")
	case strings.HasPrefix(pathItem, "/xpack"):
		dbFile = path.Join(global.CONF.Base.InstallDir, "1panel/db/xpack.db")
	default:
		dbFile = path.Join(global.CONF.Base.InstallDir, "1panel/db/agent.db")
	}

	db, _ := gorm.Open(sqlite.Open(dbFile), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxIdleTime(15 * time.Minute)
	sqlDB.SetConnMaxLifetime(time.Hour)
	return db, nil
}

func closeDB(db *gorm.DB) {
	sqlDB, err := db.DB()
	if err != nil {
		return
	}
	_ = sqlDB.Close()
}

func parseMultipart(formData []byte, contentType string) (map[string]interface{}, error) {
	d, params, err := mime.ParseMediaType(contentType)
	if err != nil || d != "multipart/form-data" {
		return nil, http.ErrNotMultipart
	}
	boundary, ok := params["boundary"]
	if !ok {
		return nil, http.ErrMissingBoundary
	}
	reader := multipart.NewReader(bytes.NewReader(formData), boundary)
	ret := make(map[string]interface{})

	f, err := reader.ReadForm(32 << 20)
	if err != nil {
		return nil, err
	}

	for k, v := range f.Value {
		if len(v) > 0 {
			ret[k] = v[0]
		}
	}
	for k, v := range f.File {
		if len(v) > 0 {
			ret[k] = v[0].Filename
		}
	}
	return ret, nil
}
