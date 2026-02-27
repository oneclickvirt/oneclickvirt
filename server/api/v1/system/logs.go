package system

import (
	"bufio"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"oneclickvirt/model/common"

	"github.com/gin-gonic/gin"
)

const logBaseDir = "./storage/logs"

// LogDateInfo 一个日期目录下的日志类型列表
type LogDateInfo struct {
	Date  string   `json:"date"`
	Types []string `json:"types"`
}

// LogDatesResponse 日志目录结构响应
type LogDatesResponse struct {
	Dates     []LogDateInfo `json:"dates"`
	RootFiles []string      `json:"root_files"`
}

// GetLogDates 列出所有可用的日志日期目录及根目录日志文件
func GetLogDates(c *gin.Context) {
	entries, err := os.ReadDir(logBaseDir)
	if err != nil {
		c.JSON(http.StatusOK, common.Response{Code: 500, Msg: "读取日志目录失败: " + err.Error()})
		return
	}

	var dates []LogDateInfo
	var rootFiles []string

	for _, entry := range entries {
		if entry.IsDir() {
			if matched, _ := filepath.Match("????-??-??", entry.Name()); matched {
				// 读取该日期目录下的日志文件
				subEntries, err := os.ReadDir(filepath.Join(logBaseDir, entry.Name()))
				if err != nil {
					continue
				}
				var types []string
				for _, sub := range subEntries {
					if !sub.IsDir() && strings.HasSuffix(sub.Name(), ".log") {
						types = append(types, strings.TrimSuffix(sub.Name(), ".log"))
					}
				}
				sort.Strings(types)
				dates = append(dates, LogDateInfo{Date: entry.Name(), Types: types})
			}
		} else if strings.HasSuffix(entry.Name(), ".log") {
			rootFiles = append(rootFiles, entry.Name())
		}
	}

	// 按日期降序排序（最新在前）
	sort.Slice(dates, func(i, j int) bool {
		return dates[i].Date > dates[j].Date
	})
	sort.Strings(rootFiles)

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "成功",
		Data: LogDatesResponse{
			Dates:     dates,
			RootFiles: rootFiles,
		},
	})
}

// GetLogContent 读取指定日志文件的最后 N 行
//
// Query 参数：
//   - date  日期字符串（如 "2026-02-26"）；为空时表示根目录日志
//   - file  日志文件名（有 date 时填级别如 "error"；无 date 时填完整文件名如 "server.log"）
//   - tail  从末尾读取的行数，默认 200，最大 5000
func GetLogContent(c *gin.Context) {
	date := strings.TrimSpace(c.Query("date"))
	file := strings.TrimSpace(c.Query("file"))
	tailStr := c.DefaultQuery("tail", "200")

	if file == "" {
		c.JSON(http.StatusOK, common.Response{Code: 400, Msg: "缺少 file 参数"})
		return
	}

	// 防止路径穿越
	if strings.Contains(file, "/") || strings.Contains(file, "\\") || strings.Contains(file, "..") {
		c.JSON(http.StatusOK, common.Response{Code: 403, Msg: "非法文件名"})
		return
	}
	if date != "" && (strings.Contains(date, "/") || strings.Contains(date, "\\") || strings.Contains(date, "..")) {
		c.JSON(http.StatusOK, common.Response{Code: 403, Msg: "非法日期"})
		return
	}

	tail, err := strconv.Atoi(tailStr)
	if err != nil || tail <= 0 {
		tail = 200
	}
	if tail > 5000 {
		tail = 5000
	}

	var logPath string
	if date == "" {
		// 根目录日志文件，直接使用完整文件名
		logPath = filepath.Join(logBaseDir, file)
	} else {
		// 日期目录下的日志，文件名为 "级别.log"
		logPath = filepath.Join(logBaseDir, date, file+".log")
	}

	// 路径安全校验
	absBase, _ := filepath.Abs(logBaseDir)
	absPath, _ := filepath.Abs(logPath)
	if !strings.HasPrefix(absPath, absBase+string(os.PathSeparator)) && absPath != absBase {
		c.JSON(http.StatusOK, common.Response{Code: 403, Msg: "非法路径"})
		return
	}

	lines, err := readTailLines(logPath, tail)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusOK, common.Response{Code: 404, Msg: "日志文件不存在"})
		} else {
			c.JSON(http.StatusOK, common.Response{Code: 500, Msg: "读取日志文件失败: " + err.Error()})
		}
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "成功",
		Data: gin.H{
			"content": strings.Join(lines, "\n"),
			"lines":   len(lines),
		},
	})
}

// readTailLines 读取文件最后 n 行，使用环形缓冲避免全量内存占用
func readTailLines(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// 使用环形缓冲只保留最后 n 行
	ring := make([]string, n)
	idx := 0
	count := 0

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 512*1024)
	scanner.Buffer(buf, 512*1024)

	for scanner.Scan() {
		ring[idx%n] = scanner.Text()
		idx++
		count++
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if count == 0 {
		return []string{}, nil
	}

	// 从环形缓冲中按顺序还原
	if count <= n {
		return ring[:count], nil
	}
	result := make([]string, n)
	for i := 0; i < n; i++ {
		result[i] = ring[(idx+i)%n]
	}
	return result, nil
}
