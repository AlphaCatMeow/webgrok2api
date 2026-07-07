# API Reference

grok2api exposes an OpenAI-compatible and Anthropic-compatible REST API. Default listen address: `http://0.0.0.0:8000`.

## Authentication

| Method | Header / Parameter |
|---|---|
| Bearer token | `Authorization: Bearer <api_key>` |
| x-api-key | `x-api-key: <api_key>` |

When `app.api_key` is empty in config, authentication is **disabled** (open mode).

Admin endpoints use `app.app_key` instead, and additionally accept `?app_key=<key>` as a query parameter.

---

## Chat Completions (OpenAI-compatible)

### `POST /v1/chat/completions`

The main endpoint. Dispatches internally by model capability: grok.com chat, console.x.ai chat, image generation, image editing, or video generation — all through the same request shape.

#### Request Body

```json
{
  "model": "grok-4.20-0309-non-reasoning",
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "Hello!"}
  ],
  "stream": true,
  "temperature": 0.8,
  "top_p": 0.95,
  "reasoning_effort": "medium"
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `model` | string | **required** | Model name (see [Models](#models) below) |
| `messages` | array | **required** | OpenAI message format. Supports `system`, `user`, `assistant`, `tool` roles. Content can be a string or an array of content parts (`text`, `image_url`) |
| `stream` | bool | `true` (config) | Enable SSE streaming |
| `temperature` | float | `0.8` | Sampling temperature |
| `top_p` | float | `0.95` | Nucleus sampling |
| `reasoning_effort` | string | _(config)_ | `"none"` disables thinking tokens; `"low"`, `"medium"`, `"high"`, `"xhigh"` for console models; omit to use `features.thinking` default |
| `max_tokens` | int | — | Max output tokens |
| `tools` | array | — | Tool definitions (function calling) |
| `tool_choice` | any | — | Tool selection strategy |
| `image_config` | object | — | Image generation options (`n`, `size`, `response_format`) when using an image model |
| `video_config` | object | — | Video generation options (`seconds`, `size`) when using a video model |

#### Messages with Images

```json
{
  "model": "grok-4.20-0309-non-reasoning",
  "messages": [
    {
      "role": "user",
      "content": [
        {"type": "text", "text": "What's in this image?"},
        {"type": "image_url", "image_url": {"url": "https://example.com/photo.jpg"}}
      ]
    }
  ]
}
```

`image_url` also accepts `data:image/jpeg;base64,...` data URIs.

#### Streaming Response (SSE)

```
data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","created":1719500000,"model":"grok-4.20-0309-non-reasoning","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}

data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","created":1719500000,"model":"grok-4.20-0309-non-reasoning","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","created":1719500000,"model":"grok-4.20-0309-non-reasoning","choices":[{"index":0,"delta":{"content":"!"},"finish_reason":null}]}

data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","created":1719500000,"model":"grok-4.20-0309-non-reasoning","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
```

When `reasoning_effort` is enabled, thinking tokens appear as:
```json
{"delta": {"reasoning_content": "Let me think about this..."}}
```

#### Non-Streaming Response

```json
{
  "id": "chatcmpl-xxx",
  "object": "chat.completion",
  "created": 1719500000,
  "model": "grok-4.20-0309-non-reasoning",
  "choices": [{
    "index": 0,
    "message": {
      "role": "assistant",
      "content": "Hello! How can I help you?",
      "reasoning_content": "The user said hello..."
    },
    "finish_reason": "stop"
  }],
  "usage": {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
}
```

#### Retry Behavior

On upstream failure (429, 401, 503), the gateway automatically retries with a different account. Max retries: `retry.max_retries` (default 1) for quota strategy, 5 for random strategy.

---

## Responses API (OpenAI-compatible)

### `POST /v1/responses`

OpenAI Responses API format. Console models route to console.x.ai; others go through grok.com.

```json
{
  "model": "grok-4.3-console",
  "input": "Explain quantum computing",
  "instructions": "You are a physics teacher.",
  "stream": false,
  "reasoning": {"effort": "high"}
}
```

| Field | Type | Description |
|---|---|---|
| `model` | string | **required** |
| `input` | string or array | User input (string, or array of message/function_call/function_call_output items) |
| `instructions` | string | System prompt |
| `stream` | bool | Enable SSE streaming |
| `reasoning` | object | `{"effort": "low"|"medium"|"high"|"xhigh"}` |
| `temperature` | float | Sampling temperature |
| `top_p` | float | Nucleus sampling |
| `tools` | array | Tool definitions |
| `tool_choice` | any | Tool selection |

---

## Anthropic-compatible

### `POST /v1/messages`

Accepts Anthropic message format and converts internally.

```json
{
  "model": "grok-4.20-0309-non-reasoning",
  "max_tokens": 4096,
  "system": "You are helpful.",
  "messages": [
    {"role": "user", "content": "Hello!"}
  ],
  "thinking": {"type": "enabled"},
  "stream": true
}
```

| Field | Type | Description |
|---|---|---|
| `model` | string | **required** |
| `messages` | array | Anthropic message format (supports `text`, `image`, `tool_use`, `tool_result` content blocks) |
| `system` | string or array | System prompt (string or array of `{type: "text", text: "..."}`) |
| `max_tokens` | int | Max output tokens |
| `stream` | bool | Enable SSE streaming |
| `thinking` | object | `{"type": "enabled"}` to emit thinking tokens |
| `temperature` | float | Sampling temperature |
| `top_p` | float | Nucleus sampling |
| `tools` | array | Tool definitions |
| `tool_choice` | any | Tool selection |

#### Non-Streaming Response

```json
{
  "id": "msg_xxx",
  "type": "message",
  "role": "assistant",
  "model": "grok-4.20-0309-non-reasoning",
  "content": [{"type": "text", "text": "Hello!"}],
  "stop_reason": "end_turn",
  "stop_sequence": null,
  "usage": {"input_tokens": 0, "output_tokens": 0}
}
```

#### Streaming Events

```
event: message_start
data: {"type":"message_start","message":{...}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":0}}

event: message_stop
data: {"type":"message_stop"}
```

---

## Image Generation

### `POST /v1/images/generations`

OpenAI-compatible image generation endpoint. Use this for `grok-imagine-image-lite` (HTTP-based).

> **Note**: `grok-imagine-image` and `grok-imagine-image-pro` use WebSocket-based real-time generation. They can also be called through `POST /v1/chat/completions` — the gateway auto-detects the model and routes accordingly, returning generated images as `![image](url)` in the response content.

```json
{
  "model": "grok-imagine-image",
  "prompt": "A sunset over mountains",
  "n": 1,
  "size": "1024x1024",
  "response_format": "url"
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `model` | string | **required** | `grok-imagine-image-lite` (HTTP), `grok-imagine-image` (WebSocket), or `grok-imagine-image-pro` (WebSocket). WebSocket models are recommended via `/v1/chat/completions` for real-time streaming. |
| `prompt` | string | **required** | Image description |
| `n` | int | `1` | Number of images (max 4 for lite, 10 for others) |
| `size` | string | — | Image dimensions |
| `response_format` | string | `"url"` | `"url"` or `"b64_json"` |

#### Response

```json
{
  "created": 1719500000,
  "data": [
    {"url": "https://xxx.grok.com/image.jpg"}
  ]
}
```

### `POST /v1/images/edits`

Multipart image editing.

```bash
curl -X POST http://localhost:8000/v1/images/edits \
  -H "Authorization: Bearer YOUR_KEY" \
  -F "model=grok-imagine-image-edit" \
  -F "prompt=Add a rainbow in the sky" \
  -F "image[]=@photo.jpg" \
  -F "response_format=url"
```

| Field | Type | Description |
|---|---|---|
| `model` | string | **required** — must be `grok-imagine-image-edit` |
| `prompt` | string | **required** — editing instruction |
| `image[]` | file | **required** — one or more source images |
| `response_format` | string | `"url"` (default) or `"b64_json"` |

---

## Video Generation

### `POST /v1/videos`

Async video creation. Returns a job immediately; poll for completion.

```bash
curl -X POST http://localhost:8000/v1/videos \
  -H "Authorization: Bearer YOUR_KEY" \
  -F "model=grok-imagine-video" \
  -F "prompt=A cat playing piano" \
  -F "seconds=6" \
  -F "size=720x1280"
```

| Field | Type | Default | Description |
|---|---|---|---|
| `model` | string | **required** | Must be `grok-imagine-video` |
| `prompt` | string | **required** | Video description |
| `seconds` | int | `6` | Duration: 6, 10, 12, 16, or 20 |
| `size` | string | `"720x1280"` | Video dimensions |

#### Response

```json
{
  "id": "video_xxx",
  "object": "video",
  "created_at": 1719500000,
  "status": "queued",
  "model": "grok-imagine-video",
  "progress": 0,
  "prompt": "A cat playing piano",
  "seconds": "6",
  "size": "720x1280",
  "quality": "standard"
}
```

### `GET /v1/videos/{id}`

Poll video job status. When `status` is `"completed"`, `video_url` is populated.

### `GET /v1/videos/{id}/content`

Download the completed video file (MP4).

---

## Models

### `GET /v1/models`

Returns available models based on active account pools.

### `GET /v1/models/{id}`

Get a single model by ID.

### Available Models

#### grok.com Chat Models

| Model | Mode | Tier | Notes |
|---|---|---|---|
| `grok-4.20-0309-non-reasoning` | auto | super | Default balanced |
| `grok-4.20-0309-non-reasoning-reasoning` | expert | super | Deep reasoning |
| `grok-4.20-0309-non-reasoning-non-reasoning` | fast | basic | Fast, no reasoning |
| `grok-4.20-0309-non-reasoning-super` | auto | super | Super tier |
| `grok-4.20-0309-non-reasoning-reasoning-super` | expert | super | Super reasoning |
| `grok-4.20-0309-non-reasoning-non-reasoning-super` | fast | super | Super fast |
| `grok-4.20-0309-non-reasoning-heavy` | auto | heavy | Heavy tier |
| `grok-4.20-0309-non-reasoning-reasoning-heavy` | expert | heavy | Heavy reasoning |
| `grok-4.20-0309-non-reasoning-non-reasoning-heavy` | fast | heavy | Heavy fast |
| `grok-4.20-multi-agent-0309` | heavy | heavy | Multi-agent |
| `grok-4.20-fast` | fast | basic | PreferBest |
| `grok-4.3-fast` | fast | basic | PreferBest |
| `grok-4.20-auto` | auto | super | PreferBest |
| `grok-4.20-expert` | expert | super | PreferBest |
| `grok-4.20-heavy` | heavy | heavy | PreferBest |
| `grok-4.3-beta` | grok43 | super | Beta |

#### Console Models (console.x.ai)

| Model | Thinking Level |
|---|---|
| `grok-4.3-console` | default |
| `grok-4.3-low` | low |
| `grok-4.3-medium` | medium |
| `grok-4.3-high` | high |
| `grok-4.20-0309-non-reasoning-reasoning-console` | default |
| `grok-4.20-0309-non-reasoning-console` | default |
| `grok-4.20-0309-non-reasoning-console` | default |
| `grok-4.20-multi-agent-console` | default |
| `grok-4.20-multi-agent-low` | low |
| `grok-4.20-multi-agent-medium` | medium |
| `grok-4.20-multi-agent-high` | high |
| `grok-4.20-multi-agent-xhigh` | xhigh |
| `grok-build-console` | default |

#### Media Models

| Model | Capability | Transport |
|---|---|---|
| `grok-imagine-image-lite` | Image generation (basic) | HTTP (grok.com chat) |
| `grok-imagine-image` | Image generation | WebSocket (real-time streaming) |
| `grok-imagine-image-pro` | Image generation (pro) | WebSocket (real-time streaming) |
| `grok-imagine-image-edit` | Image editing | HTTP (grok.com chat) |
| `grok-imagine-video` | Video generation | HTTP (grok.com chat) |

---

## Utility Endpoints

### `GET /health`

```json
{"status": "ok"}
```

### `GET /meta`

```json
{"version": "1.0.0"}
```

### `GET /v1/files/image?id=<file_id>`

Serve a cached image by file ID. Returns JPEG or PNG.

### `GET /v1/files/video?id=<file_id>`

Serve a cached video by file ID. Returns MP4.

---

## Admin API

All admin endpoints require `app.app_key` authentication via `Authorization: Bearer <app_key>` or `?app_key=<key>`.

### Config

| Method | Path | Description |
|---|---|---|
| `GET` | `/admin/api/config` | Get current config |
| `POST` | `/admin/api/config` | Update config (persisted to user config file) |

### Token Management

| Method | Path | Description |
|---|---|---|
| `GET` | `/admin/api/tokens` | List all tokens (paginated) |
| `POST` | `/admin/api/tokens/add` | Add tokens |
| `POST` | `/admin/api/tokens` | Replace all tokens in a pool |
| `DELETE` | `/admin/api/tokens` | Delete tokens |
| `DELETE` | `/admin/api/tokens/invalid` | Delete invalid/expired tokens |
| `PUT` | `/admin/api/tokens/edit` | Edit token properties |
| `POST` | `/admin/api/tokens/disabled` | Toggle disabled state |
| `POST` | `/admin/api/tokens/disabled/batch` | Batch toggle disabled |

### Pool & Batch Operations

| Method | Path | Description |
|---|---|---|
| `PUT` | `/admin/api/pool` | Replace entire pool |
| `POST` | `/admin/api/batch/nsfw` | Batch NSFW toggle |
| `POST` | `/admin/api/batch/refresh` | Trigger quota refresh |
| `POST` | `/admin/api/batch/cache-clear` | Clear all caches |

### Status & Sync

| Method | Path | Description |
|---|---|---|
| `GET` | `/admin/api/verify` | Verify admin auth |
| `GET` | `/admin/api/status` | Get system status |
| `GET` | `/admin/api/storage` | Get storage info |
| `POST` | `/admin/api/sync` | Force directory sync |

### Assets

| Method | Path | Description |
|---|---|---|
| `GET` | `/admin/api/assets` | List assets |
| `POST` | `/admin/api/assets/delete-item` | Delete a specific asset |
| `POST` | `/admin/api/assets/clear-token` | Clear all assets for a token |

### Media Cache

| Method | Path | Description |
|---|---|---|
| `GET` | `/admin/api/cache` | Cache statistics |
| `GET` | `/admin/api/cache/list` | List cached items |
| `POST` | `/admin/api/cache/clear` | Clear all cache |
| `POST` | `/admin/api/cache/item/delete` | Delete a cache item |
| `POST` | `/admin/api/cache/items/delete` | Delete multiple items |

---

## Quick Start Examples

### curl — Basic Chat

```bash
curl http://localhost:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "grok-4.20-0309-non-reasoning",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

### curl — Streaming Chat

```bash
curl http://localhost:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "grok-4.20-0309-non-reasoning",
    "messages": [{"role": "user", "content": "Write a poem"}],
    "stream": true
  }'
```

### curl — Console Model with Thinking

```bash
curl http://localhost:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "grok-4.3-high",
    "messages": [{"role": "user", "content": "Prove the Riemann hypothesis"}],
    "reasoning_effort": "high"
  }'
```

### curl — Image Generation

```bash
curl http://localhost:8000/v1/images/generations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "grok-imagine-image",
    "prompt": "A futuristic city at night",
    "n": 2
  }'
```

### curl — Image Generation via Chat (WebSocket)

```bash
curl http://localhost:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "grok-imagine-image",
    "messages": [{"role": "user", "content": "A cat in space"}],
    "image_config": {"n": 2, "size": "1024x1024"}
  }'
```

> WebSocket models (`grok-imagine-image`, `grok-imagine-image-pro`) generate images in real-time with progress updates. Results are returned as `![image](url)` in the response content.

### curl — Anthropic Format

```bash
curl http://localhost:8000/v1/messages \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "grok-4.20-0309-non-reasoning",
    "max_tokens": 4096,
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

### Python (openai SDK)

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:8000/v1",
    api_key="YOUR_API_KEY",
)

# Non-streaming
response = client.chat.completions.create(
    model="grok-4.20-0309-non-reasoning",
    messages=[{"role": "user", "content": "Hello!"}],
)
print(response.choices[0].message.content)

# Streaming
stream = client.chat.completions.create(
    model="grok-4.20-0309-non-reasoning",
    messages=[{"role": "user", "content": "Write a haiku"}],
    stream=True,
)
for chunk in stream:
    if chunk.choices[0].delta.content:
        print(chunk.choices[0].delta.content, end="")
```

### Python (anthropic SDK)

```python
import anthropic

client = anthropic.Anthropic(
    base_url="http://localhost:8000",
    api_key="YOUR_API_KEY",
)

message = client.messages.create(
    model="grok-4.20-0309-non-reasoning",
    max_tokens=4096,
    messages=[{"role": "user", "content": "Hello!"}],
)
print(message.content[0].text)
```

---

## Error Responses

All errors follow this format:

```json
{
  "error": {
    "message": "Description of what went wrong",
    "type": "validation_error",
    "code": "model_not_found",
    "param": "model",
    "status": 400
  }
}
```

| Error Type | HTTP Status | Common Causes |
|---|---|---|
| `validation_error` | 400 | Invalid model, missing required fields, bad JSON |
| `authentication_error` | 401 | Missing or invalid API key |
| `rate_limit_error` | 429 | No available accounts, all quotas exhausted |
| `upstream_error` | 502 | Grok upstream returned an error |
| `server_error` | 500 | Internal server error |
