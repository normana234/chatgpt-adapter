package response

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"chatgpt-adapter/core/common"
	"chatgpt-adapter/core/common/vars"
	"chatgpt-adapter/core/gin/model"
	"chatgpt-adapter/core/logger"
	"github.com/gin-gonic/gin"
)

var (
	stop        = "stop"
	toolCalls   = "tool_calls"
	canResponse = "__can-response__"

	EOF = "<CHAR_trun>"

	UnauthorizedError = fmt.Errorf("unauthorized error")
)

func MessageValidator(ctx *gin.Context) bool {
	completion := common.GetGinCompletion(ctx)
	messageL := len(completion.Messages)
	if messageL == 0 {
		Error(ctx, -1, "[] is too short - 'messages'")
		return false
	}

	condition := func(expr string) string {
		switch expr {
		case "user", "system", "assistant", "tool", "function":
			return expr
		default:
			return ""
		}
	}

	for index := 0; index < messageL; index++ {
		message := completion.Messages[index]
		role := condition(message.GetString("role"))
		if role == "" {
			str := fmt.Sprintf("'%s' is not in ['system', 'assistant', 'user', 'tool', 'function'] - 'messages.[%d].role'", message["role"], index)
			Error(ctx, -1, str)
			return false
		}
	}
	return true
}

func Error(ctx *gin.Context, code int, err interface{}) {
	ctx.Set(canResponse, "No!")
	if code == -1 {
		code = http.StatusInternalServerError
	}

	if str, ok := err.(string); ok {
		ctx.JSON(code, gin.H{
			"error": map[string]string{
				"message": str,
			},
		})
		return
	}

	if e, ok := err.(error); ok {
		ctx.JSON(code, gin.H{
			"error": map[string]string{
				"message": e.Error(),
			},
		})
		return
	}

	ctx.JSON(code, gin.H{
		"error": map[string]string{
			"message": fmt.Sprintf("%v", err),
		},
	})
}

func Response(ctx *gin.Context, mod, content string) {
	ctx.Set(canResponse, "No!")
	created := time.Now().Unix()
	usage := common.GetGinCompletionUsage(ctx)
	ctx.JSON(http.StatusOK, model.Response{
		Model:   mod,
		Created: created,
		Id:      fmt.Sprintf("chatcmpl-%d", created),
		Object:  "chat.completion",
		Choices: []model.Choice{
			{
				Index: 0,
				Message: &struct {
					Role      string                    `json:"role,omitempty"`
					Content   string                    `json:"content,omitempty"`
					ToolCalls []model.Keyv[interface{}] `json:"tool_calls,omitempty"`
				}{"assistant", content, nil},
				FinishReason: &stop,
			},
		},
		Usage: usage,
	})
}

func Echo(ctx *gin.Context, mode, content string, sse bool) {
	if !sse {
		Response(ctx, mode, content)
	} else {
		created := time.Now().Unix()
		pos := 0
		runStr := []rune(content)
		step := 1000

		for {
			// fix: 太长了有些流客户端无法接收
			contentL := len(runStr[pos:])
			if contentL > step {
				SSEResponse(ctx, mode, string(runStr[pos:pos+step]), created)
				pos += step
				continue
			}

			SSEResponse(ctx, mode, string(runStr[pos:]), created)
			break
		}

		SSEResponse(ctx, mode, "[DONE]", created)
	}
}

func SSEResponse(ctx *gin.Context, mod, content string, created int64) {
	ctx.Set(canResponse, "No!")
	setSSEHeader(ctx)
	if content == "" {
		return
	}

	done := false
	finishReason := ""
	usage := common.GetGinCompletionUsage(ctx)

	if content == "[DONE]" {
		done = true
		content = ""
		finishReason = "stop"
	}

	response := model.Response{
		Model:   mod,
		Created: created,
		Id:      fmt.Sprintf("chatcmpl-%d", created),
		Object:  "chat.completion.chunk",
		Choices: []model.Choice{
			{
				Index: 0,
				Delta: &struct {
					Role      string                    `json:"role,omitempty"`
					Content   string                    `json:"content,omitempty"`
					ToolCalls []model.Keyv[interface{}] `json:"tool_calls,omitempty"`
				}{"assistant", content, nil},
			},
		},
	}

	if finishReason != "" {
		response.Usage = usage
		response.Choices[0].FinishReason = &finishReason
	}

	Event(ctx, "", response)

	if done {
		time.Sleep(100 * time.Millisecond)
		Event(ctx, "", "[DONE]")
	}
}

func ToolCallResponse(ctx *gin.Context, mod, name, args string) {
	ctx.Set(canResponse, "No!")
	created := time.Now().Unix()
	usage := common.GetGinCompletionUsage(ctx)

	ctx.JSON(http.StatusOK, model.Response{
		Model:   mod,
		Created: created,
		Id:      fmt.Sprintf("chatcmpl-%d", created),
		Object:  "chat.completion",
		Choices: []model.Choice{
			{
				Index: 0,
				Message: &struct {
					Role      string                    `json:"role,omitempty"`
					Content   string                    `json:"content,omitempty"`
					ToolCalls []model.Keyv[interface{}] `json:"tool_calls,omitempty"`
				}{
					Role: "assistant",
					ToolCalls: []model.Keyv[interface{}]{
						{
							"id":   "call_" + hex(5),
							"type": "function",
							"function": map[string]string{
								"name":      name,
								"arguments": args,
							},
						},
					},
				},
				FinishReason: &stop,
			},
		},
		Usage: usage,
	})
}

func SSEToolCallResponse(ctx *gin.Context, mod, name, args string, created int64) {
	ctx.Set(canResponse, "No!")
	setSSEHeader(ctx)
	usage := common.GetGinCompletionUsage(ctx)

	response := model.Response{
		Model:   mod,
		Created: created,
		Id:      fmt.Sprintf("chatcmpl-%d", created),
		Object:  "chat.completion.chunk",
		Choices: []model.Choice{
			{Index: 0},
		},
	}

	toolCall := make(map[string]interface{})
	toolCall["index"] = 0
	toolCall["type"] = "function"
	toolCall["id"] = "call_" + hex(5)
	toolCall["function"] = map[string]string{"name": name, "arguments": ""}
	response.Choices[0].Delta = &struct {
		Role      string                    `json:"role,omitempty"`
		Content   string                    `json:"content,omitempty"`
		ToolCalls []model.Keyv[interface{}] `json:"tool_calls,omitempty"`
	}{
		Role:      "assistant",
		ToolCalls: []model.Keyv[interface{}]{toolCall},
	}

	Event(ctx, "", response)

	delete(toolCall, "id")
	delete(toolCall, "type")
	toolCall["function"] = map[string]string{"arguments": args}
	response.Choices[0].Delta.ToolCalls[0] = toolCall
	response.Choices[0].Delta.Role = ""
	Event(ctx, "", response)

	response.Choices[0].FinishReason = &toolCalls
	response.Choices[0].Delta = nil
	response.Usage = usage
	Event(ctx, "", response)

	Event(ctx, "", "[DONE]")
}

func NotResponse(ctx *gin.Context) bool {
	return ctx.GetString(canResponse) == "" && NotSSEHeader(ctx)
}

func NotSSEHeader(ctx *gin.Context) bool {
	return notHeader(ctx, "text/event-stream")
}

func notHeader(ctx *gin.Context, types ...string) bool {
	h := ctx.Writer.Header()
	t := h.Get("Content-Type")
	if t == "" {
		return true
	}
	for _, mine := range types {
		if strings.Contains(t, mine) {
			return false
		}
	}
	return true
}

func setSSEHeader(ctx *gin.Context) {
	h := ctx.Writer.Header()
	if h.Get("Content-Type") == "" {
		h.Set("Content-Type", "text/event-stream")
		h.Set("Transfer-Encoding", "chunked")
		h.Set("Cache-Control", "no-cache")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no")
	}
}

func Event(ctx *gin.Context, event string, data interface{}) {
	ctx.Set(canResponse, "No!")
	setSSEHeader(ctx)

	w := ctx.Writer
	str, ok := data.(string)
	if ok {
		layout := ""
		if event != "" {
			layout = "event: " + event + "\n"
		}

		layout = "data: %s\n\n"
		_, err := fmt.Fprintf(w, layout, str)
		if err != nil {
			logger.Error(err)
			ctx.Set(vars.GinClose, true)
			return
		}

		w.Flush()
		return
	}

	marshal, err := json.Marshal(data)
	if err != nil {
		logger.Error(err)
		ctx.Set(vars.GinClose, true)
		return
	}

	layout := ""
	if event != "" {
		layout = "event: " + event + "\n"
	}
	layout += "data: %s\n\n"
	_, err = fmt.Fprintf(w, layout, marshal)
	if err != nil {
		logger.Error(err)
		ctx.Set(vars.GinClose, true)
		return
	}
	w.Flush()
}

func hex(n int) string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	var runes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890")
	bytes := make([]rune, n)
	for i := range bytes {
		bytes[i] = runes[r.Intn(len(runes))]
	}
	return string(bytes)
}
