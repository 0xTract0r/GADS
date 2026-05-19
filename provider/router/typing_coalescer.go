/*
 * This file is part of GADS.
 *
 * Copyright (c) 2022-2025 Nikola Shabanov
 *
 * This source code is licensed under the GNU Affero General Public License v3.0.
 * You may obtain a copy of the license at https://www.gnu.org/licenses/agpl-3.0.html
 */

package router

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"GADS/provider/devices"
)

// typingCoalescer 把 hub-ui 远程键盘连续单字符 POST /typeText 合并成
// 1-2 次大 batch 调用 WDA /wda/keys，显著降低端到端延迟。
//
// 背景：hub-ui 控制台的物理键监听对每个 keydown 都立刻发一次
// POST /device/<udid>/typeText {"text":"x"}，WDA 一次 IPC 约 300-500ms，
// 串行排队下连续输入 20 个字符会累计 6-10s。
// 本协调器为每个 UDID 维护一个 in-flight 批，把同时间窗内到达的字符
// 合并成一次 wda/keys 请求，所有等待者共享同一份 response。
//
// 协议语义：保持调用方接口不变，调用方仍然得到一份合法的 *http.Response，
// 但 response body 是这一次 batch flush 的真实 WDA 返回内容（即“你的字符
// 已经被一次 batch type 进去”）。

const (
	// 字符空闲到达窗口；人类连续敲键通常 keydown 间隔 80-150ms，
	// 实测 hub-ui 用 page.keyboard.type 的最快 delay 也在 60ms 量级，
	// 这里取 90ms 让相邻字符大概率落进同一个 batch。
	typingCoalesceIdle = 90 * time.Millisecond
	// 单次 batch 最大字符数；超过强制 flush，防止过长 IPC。
	typingCoalesceMaxBatch = 64
	// 单次 batch 最长堆积时间，避免长按某键时 idle 永远不到导致饿死。
	typingCoalesceMaxWait = 350 * time.Millisecond
)

// 允许测试替换实际的下游执行函数。
var typingDownstream = func(dev devices.PlatformDevice, chars []string) (*http.Response, error) {
	payload := struct {
		Value []string `json:"value"`
	}{Value: chars}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return wdaSessionRequestWithRetry(dev, http.MethodPost, "wda/keys", body)
}

type deviceTyper struct {
	dev      devices.PlatformDevice
	mu       sync.Mutex
	pending  []string // 等待被发送的字符
	flushing bool
	firstAt  time.Time
}

func (t *deviceTyper) submit(text string) (*http.Response, error) {
	// 拆字符；保持和原 executeTypeText 一样的逐 rune 切分。
	if text == "" {
		// 空字符串走原始路径，让 WDA 报错保持一致。
		return typingDownstream(t.dev, []string{})
	}
	chars := make([]string, 0, len(text))
	for _, r := range text {
		chars = append(chars, string(r))
	}

	t.mu.Lock()
	if len(t.pending) == 0 {
		t.firstAt = time.Now()
	}
	t.pending = append(t.pending, chars...)
	startFlusher := !t.flushing
	if startFlusher {
		t.flushing = true
	}
	t.mu.Unlock()

	if startFlusher {
		go t.flushLoop()
	}

	// 立即返回 ack，让 hub-ui 立刻发下一个 keystroke，从而让 flusher 在 idle
	// 窗口内累积更多字符。下游 wda/keys 失败只会被 provider 自己 log，因为
	// hub-ui 远程键盘不消费 response body，仅依赖 HTTP 状态做错误提示。
	ack := []byte(`{"value":null}`)
	return buildSyntheticResponse(http.StatusOK, http.Header{
		"Content-Type": []string{"application/json"},
	}, ack), nil
}

func (t *deviceTyper) flushLoop() {
	for {
		// 每轮都等一个 idle 窗口：让相邻字符尽量进同一 batch。
		// hardWait 防止极端情况下字符稳定流入导致 idle 永远不到。
		t.mu.Lock()
		first := t.firstAt
		t.mu.Unlock()

		idleTimer := time.NewTimer(typingCoalesceIdle)
		hardCap := time.Until(first.Add(typingCoalesceMaxWait))
		if hardCap < 0 {
			hardCap = 0
		}
		hardTimer := time.NewTimer(hardCap)

		flushNow := false
		for !flushNow {
			select {
			case <-idleTimer.C:
				flushNow = true
			case <-hardTimer.C:
				flushNow = true
			}
			t.mu.Lock()
			batchSize := len(t.pending)
			t.mu.Unlock()
			if batchSize >= typingCoalesceMaxBatch {
				flushNow = true
			}
		}
		if !idleTimer.Stop() {
			select {
			case <-idleTimer.C:
			default:
			}
		}
		if !hardTimer.Stop() {
			select {
			case <-hardTimer.C:
			default:
			}
		}

		// 拿出当前 pending，发请求；fire-and-forget 模式下不需要把结果回传调用方。
		t.mu.Lock()
		batch := t.pending
		t.pending = nil
		t.mu.Unlock()

		resp, err := typingDownstream(t.dev, batch)
		if err != nil {
			t.dev.GetLogger().LogError("appium_interact",
				"typing_coalescer: batch wda/keys failed: "+err.Error())
		} else {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode >= 400 {
				t.dev.GetLogger().LogError("appium_interact",
					"typing_coalescer: batch wda/keys non-2xx: "+string(body))
			}
		}

		// 看是否有新的字符在 flush 期间又被压进来。
		t.mu.Lock()
		if len(t.pending) == 0 {
			t.flushing = false
			t.mu.Unlock()
			return
		}
		// 有新字符，复用 flusher 继续下一轮。
		t.firstAt = time.Now()
		t.mu.Unlock()
	}
}

func buildSyntheticResponse(status int, header http.Header, body []byte) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(body)),
		// 给 reader 一个内容长度以兼容部分调用方。
		ContentLength: int64(len(body)),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
	}
}

var typingRegistry sync.Map // udid -> *deviceTyper

func coalescedTypeTextIOS(dev devices.PlatformDevice, text string) (*http.Response, error) {
	udid := dev.GetUDID()
	if v, ok := typingRegistry.Load(udid); ok {
		return v.(*deviceTyper).submit(text)
	}
	newT := &deviceTyper{dev: dev}
	actual, _ := typingRegistry.LoadOrStore(udid, newT)
	return actual.(*deviceTyper).submit(text)
}
