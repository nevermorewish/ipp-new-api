package relay

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// newPassThroughRequestBody preserves ordinary pass-through requests byte for
// byte. Enterprise aliases are the exception: their client-facing model must
// be replaced with the final channel model before the raw body goes upstream.
func newPassThroughRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, int64, io.Closer, error) {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, 0, nil, err
	}
	if info == nil || strings.TrimSpace(info.RequestedModelName) == "" {
		return common.ReaderOnly(storage), storage.Size(), nil, nil
	}

	upstreamModel := strings.TrimSpace(info.UpstreamModelName)
	if upstreamModel == "" {
		return nil, 0, nil, errors.New("enterprise alias has no upstream model")
	}
	contentType := strings.TrimSpace(c.GetHeader("Content-Type"))
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, 0, nil, errors.New("enterprise alias pass-through content type is invalid")
	}
	if strings.EqualFold(mediaType, "multipart/form-data") {
		return newEnterpriseAliasMultipartBody(c, upstreamModel)
	}
	mediaType = strings.ToLower(mediaType)
	if mediaType != "application/json" &&
		!(strings.HasPrefix(mediaType, "application/") && strings.HasSuffix(mediaType, "+json")) {
		return nil, 0, nil, errors.New("enterprise alias pass-through content type is unsupported")
	}
	body, err := storage.Bytes()
	if err != nil {
		return nil, 0, nil, err
	}
	if !gjson.ValidBytes(body) {
		return nil, 0, nil, errors.New("enterprise alias pass-through body is not valid JSON")
	}
	root := gjson.ParseBytes(body)
	if !root.IsObject() {
		return nil, 0, nil, errors.New("enterprise alias pass-through body must be a JSON object")
	}
	modelFields := 0
	modelField := ""
	root.ForEach(func(key, _ gjson.Result) bool {
		if strings.EqualFold(key.String(), "model") {
			modelFields++
			modelField = key.String()
		}
		return true
	})
	if modelFields == 0 {
		return common.ReaderOnly(storage), storage.Size(), nil, nil
	}
	if modelFields != 1 {
		return nil, 0, nil, errors.New("enterprise alias pass-through body must contain exactly one model field")
	}

	patched, err := sjson.SetBytes(body, modelField, upstreamModel)
	if err != nil {
		return nil, 0, nil, err
	}
	return relaycommon.NewOutboundJSONBody(patched)
}

func newEnterpriseAliasMultipartBody(c *gin.Context, upstreamModel string) (io.Reader, int64, io.Closer, error) {
	form, err := common.ParseMultipartFormReusable(c)
	if err != nil {
		return nil, 0, nil, err
	}
	defer form.RemoveAll()

	modelFields := 0
	for key, values := range form.Value {
		if strings.EqualFold(key, "model") {
			modelFields += len(values)
		}
	}
	for key, files := range form.File {
		if strings.EqualFold(key, "model") {
			modelFields += len(files)
		}
	}
	if modelFields != 1 {
		return nil, 0, nil, errors.New("enterprise alias multipart body must contain exactly one model field")
	}

	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	if err := writer.WriteField("model", upstreamModel); err != nil {
		return nil, 0, nil, err
	}
	for key, values := range form.Value {
		if strings.EqualFold(key, "model") {
			continue
		}
		for _, value := range values {
			if err := writer.WriteField(key, value); err != nil {
				return nil, 0, nil, err
			}
		}
	}
	for fieldName, fileHeaders := range form.File {
		if strings.EqualFold(fieldName, "model") {
			continue
		}
		for _, fileHeader := range fileHeaders {
			file, err := fileHeader.Open()
			if err != nil {
				return nil, 0, nil, err
			}
			header := make(textproto.MIMEHeader, len(fileHeader.Header))
			for key, values := range fileHeader.Header {
				header[key] = append([]string(nil), values...)
			}
			part, err := writer.CreatePart(header)
			if err == nil {
				_, err = io.Copy(part, file)
			}
			closeErr := file.Close()
			if err != nil {
				return nil, 0, nil, err
			}
			if closeErr != nil {
				return nil, 0, nil, closeErr
			}
		}
	}
	if err := writer.Close(); err != nil {
		return nil, 0, nil, err
	}
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	outbound, err := common.CreateBodyStorage(buffer.Bytes())
	if err != nil {
		return nil, 0, nil, err
	}
	return common.ReaderOnly(outbound), outbound.Size(), outbound, nil
}
