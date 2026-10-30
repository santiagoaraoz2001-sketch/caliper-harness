package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func trimmedTestTemplate(messages []Message) string {
	var b strings.Builder
	for _, message := range messages {
		b.WriteString("<" + message.Role + ">" + strings.TrimSpace(message.Content) + "</" + message.Role + ">\n")
	}
	return b.String() + "<assistant>"
}

func TestRulithCompletedWhitespaceSurvivesTemplateAndLiteralContinuation(t *testing.T) {
	for _, content := range []string{"PASS: observed\n", " \tPASS: café — 🌱\r\n\n", " \t\n", "PASS: no exterior whitespace", "\nCALIPER_TEMPLATE_CONTENT_0_ is user data\n"} {
		t.Run(content, func(t *testing.T) {
			messages := []Message{{Role: "system", Content: "rules"}, {Role: "user", Content: "review"}, {Role: "assistant", Content: content}, {Role: "user", Content: "one verdict"}}
			original, _ := json.Marshal(messages)
			want := "<system>rules</system>\n<user>review</user>\n<assistant>" + content + "</assistant>\n<user>one verdict</user>\n<assistant>"
			tools := rulithTestTools()
			renders, completions := 0, 0
			parts := []string{"PASS: ready ", "🌱\n"}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/apply-template":
					renders++
					var request chatRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						return
					}
					if len(request.Messages) != len(messages) || !reflect.DeepEqual(request.Tools, tools) || !reflect.DeepEqual(request.Messages[0], messages[0]) {
						t.Error("changed roles, schema or number of messages")
					}
					json.NewEncoder(w).Encode(map[string]string{"prompt": trimmedTestTemplate(request.Messages)})
				case "/completion":
					var request struct{ Prompt string }
					json.NewDecoder(r.Body).Decode(&request)
					if completions >= len(parts) {
						t.Error("extra inference")
						w.WriteHeader(500)
						return
					}
					if request.Prompt != want+strings.Join(parts[:completions], "") {
						t.Errorf("completed content or literal prefix changed: %q", request.Prompt)
					}
					nativeEvent(w, map[string]any{"content": parts[completions]})
					stop := "limit"
					if completions == 1 {
						stop = "eos"
					}
					nativeEvent(w, nativeTerminal(request.Prompt, stop, 4))
					completions++
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			c := New(srv.URL+"/v1", "qwen").WithRuntime(RuntimeRulith)
			reply, err := c.ChatTools(t.Context(), messages, tools, nil)
			if err == nil || !reply.OutputLimit || reply.Prefix == nil || reply.Prefix.Prompt != want {
				t.Fatalf("missing exact continuation: %+v %v", reply, err)
			}
			logical := c.request(messages, false)
			logical.Tools = tools
			identity, _ := json.Marshal(logical)
			if reply.Prefix.RequestSHA256 != byteHash(identity) {
				t.Fatal("render markers changed logical identity")
			}
			reply, err = c.ChatTools(WithResponseContinuation(context.Background(), reply), messages, tools, nil)
			if err != nil || reply.RawNative != strings.Join(parts, "") || renders != 1 || completions != 2 {
				t.Fatalf("continuation changed output/call count: %+v %v %d/%d", reply, err, renders, completions)
			}
			after, _ := json.Marshal(messages)
			if string(after) != string(original) {
				t.Fatal("render changed caller history")
			}
		})
	}
}

func TestRulithDamagedProtectedContentRefusesBeforeInference(t *testing.T) {
	for _, mode := range []string{"lost", "duplicated", "changed", "reordered", "extra-marker"} {
		t.Run(mode, func(t *testing.T) {
			completions := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/apply-template" {
					completions++
					w.WriteHeader(500)
					return
				}
				var request chatRequest
				json.NewDecoder(r.Body).Decode(&request)
				prompt := trimmedTestTemplate(request.Messages)
				switch mode {
				case "lost":
					prompt = "unrelated template"
				case "duplicated":
					prompt += request.Messages[1].Content
				case "changed":
					prompt = strings.ReplaceAll(prompt, "café", "cafe")
				case "reordered":
					prompt = strings.NewReplacer("_BEGIN", "_END", "_END", "_BEGIN").Replace(prompt)
				case "extra-marker":
					prompt += "CALIPER_TEMPLATE_CONTENT_0_unexpected"
				}
				json.NewEncoder(w).Encode(map[string]string{"prompt": prompt})
			}))
			defer srv.Close()
			c := New(srv.URL+"/v1", "qwen").WithRuntime(RuntimeRulith)
			_, err := c.ChatTools(t.Context(), []Message{{Role: "user", Content: "review"}, {Role: "assistant", Content: "café\n"}}, rulithTestTools(), nil)
			if err == nil || completions != 0 {
				t.Fatalf("damaged content reached inference: %v %d", err, completions)
			}
		})
	}
}

func TestRulithRenderingPreservesOnlyPlainAssistantWhitespace(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: " user whitespace follows the template \n"},
		{Role: "assistant", Content: " same answer\n"},
		{Role: "assistant", Content: " tool preface\n", ToolCalls: []ToolCall{{ID: "call-1", Name: "read", Arguments: "{}"}}},
		{Role: "tool", ToolCallID: "call-1", Content: " result\n"},
		{Role: "assistant", Content: " same answer\n", ReasoningContent: "reasoning remains separate"},
		{Role: "assistant"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chatRequest
		json.NewDecoder(r.Body).Decode(&request)
		for _, i := range []int{0, 2, 3, 5} {
			if !reflect.DeepEqual(request.Messages[i], messages[i]) {
				t.Error("changed non-plain content or tool correlation", i)
			}
		}
		if request.Messages[4].ReasoningContent != messages[4].ReasoningContent {
			t.Error("changed separate reasoning field")
		}
		json.NewEncoder(w).Encode(map[string]string{"prompt": trimmedTestTemplate(request.Messages)})
	}))
	defer srv.Close()
	got, err := New(srv.URL, "qwen").rulithRenderedPrompt(t.Context(), chatRequest{Messages: messages})
	want := trimmedTestTemplate(messages)
	want = strings.ReplaceAll(want, "<assistant>same answer</assistant>", "<assistant> same answer\n</assistant>")
	if err != nil || got != want {
		t.Fatalf("mixed roles/repeated body rendering changed: %q %v", got, err)
	}
}
