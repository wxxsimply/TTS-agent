package api

import (
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/tts-agent/internal/dataset"
	"github.com/tts-agent/internal/middleware"
	"github.com/tts-agent/internal/store"
)

const maxUploadBytes = 10 << 20

type DatasetHandler struct{ db *store.Postgres }

func NewDatasetHandler(db *store.Postgres) *DatasetHandler { return &DatasetHandler{db: db} }

func (h *DatasetHandler) List(c *gin.Context) {
	datasets, err := h.db.ListDatasets(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "数据集列表加载失败"})
		return
	}
	c.JSON(http.StatusOK, datasets)
}

func (h *DatasetHandler) Upload(c *gin.Context) {
	file, header, err := readExcelUpload(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	defer file.Close()
	columnTypes, err := readColumnTypes(c.PostForm("column_types"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	parsed, values, err := dataset.ParseExcelWithColumnTypes(file, header.Filename, middleware.GetUserID(c), columnTypes)
	if err != nil {
		var validationErr *dataset.ValidationError
		if errors.As(err, &validationErr) {
			c.JSON(http.StatusBadRequest, gin.H{"detail": validationErr.Error(), "cell_errors": validationErr.Errors})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	if err := h.db.CreateDataset(c.Request.Context(), parsed, values); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "数据写入失败，请检查表头和数据格式后重试"})
		return
	}
	c.JSON(http.StatusCreated, parsed)
}

func (h *DatasetHandler) Preview(c *gin.Context) {
	file, header, err := readExcelUpload(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	defer file.Close()
	preview, err := dataset.PreviewExcel(file, header.Filename, middleware.GetUserID(c))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, preview)
}

func readExcelUpload(c *gin.Context) (multipart.File, *multipart.FileHeader, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadBytes)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		return nil, nil, errors.New("请选择不超过 10MB 的 Excel 文件")
	}
	if strings.ToLower(filepath.Ext(header.Filename)) != ".xlsx" {
		file.Close()
		return nil, nil, errors.New("当前仅支持 .xlsx 格式的 Excel 文件")
	}
	return file, header, nil
}

func readColumnTypes(raw string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	types := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &types); err != nil {
		return nil, errors.New("字段类型设置格式无效，请重新预览文件后导入")
	}
	return types, nil
}

func (h *DatasetHandler) Delete(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "数据集编号无效"})
		return
	}
	deleted, err := h.db.DeleteDataset(c.Request.Context(), id, middleware.GetUserID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "删除数据集失败"})
		return
	}
	if !deleted {
		c.JSON(http.StatusNotFound, gin.H{"detail": "数据集不存在或无权访问"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "数据集已删除"})
}
