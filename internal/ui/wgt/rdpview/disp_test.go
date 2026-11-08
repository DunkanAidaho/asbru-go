package rdpview

// Динамическое разрешение RDP (/dynamic-resolution): ресайз окна → запрос серверу
// через канал DisplayControl. Контракт целевого размера (rdp_disp_resize_target,
// bridge.h): физические пиксели (логический размер × DPR), кламп в протокольные
// пределы DISPLAY_CONTROL_MIN/MAX_MONITOR_WIDTH/HEIGHT = 200..8192 (disp.h),
// «не изменилось» → не слать. Поведение запроса — как xf_disp
// (client/X11/xf_disp.c:501 configureNotify → queueResize → SendMonitorLayout).

import "testing"

func TestDispResizeTargetComputesPhysicalPixels(t *testing.T) {
	cases := []struct {
		name       string
		ww, wh     int
		dpr        float64
		curW, curH int
		outW, outH int
		want       bool
	}{
		{"1:1 без HiDPI", 1024, 768, 1.0, 0, 0, 1024, 768, true},
		{"DPR 2 — физические пиксели", 1024, 768, 2.0, 0, 0, 2048, 1536, true},
		{"DPR 1.5", 800, 600, 1.5, 0, 0, 1200, 900, true},
		{"0.5 округляется вверх", 333, 333, 1.5, 0, 0, 500, 500, true},
		{"DPR <= 0 считается 1", 1024, 768, 0.0, 0, 0, 1024, 768, true},
		{"кламп сверху (предел провода 8192)", 10000, 9000, 1.0, 0, 0, 8192, 8192, true},
		{"кламп снизу (минимум провода 200)", 100, 50, 1.0, 0, 0, 200, 200, true},
		{"смешанный кламп по сторонам", 100, 9000, 1.0, 0, 0, 200, 8192, true},
	}
	for _, c := range cases {
		w, h, ok := dispResizeTarget(c.ww, c.wh, c.dpr, c.curW, c.curH)
		if ok != c.want || w != c.outW || h != c.outH {
			t.Errorf("%s: widget %dx%d dpr %.2f cur %dx%d → (%d,%d,%v), ожидалось (%d,%d,%v)",
				c.name, c.ww, c.wh, c.dpr, c.curW, c.curH, w, h, ok, c.outW, c.outH, c.want)
		}
	}
}

func TestDispResizeTargetSkipsUnchanged(t *testing.T) {
	// Сервер уже рисует этот размер — layout не отправляем (xf_disp: target ==
	// lastSent → skip, xf_disp.c:192 xf_disp_settings_changed).
	if w, h, ok := dispResizeTarget(1024, 768, 1.0, 1024, 768); ok {
		t.Errorf("неизменённый размер отправлен: (%d,%d)", w, h)
	}
	// После клампа размер совпал с текущим — тоже «не изменилось»: виджет
	// 10000px клампится в 8192, а сервер уже рисует 8192.
	if w, h, ok := dispResizeTarget(10000, 10000, 1.0, 8192, 8192); ok {
		t.Errorf("клампнутый размер равен текущему, но отправлен: (%d,%d)", w, h)
	}
}

func TestDispResizeTargetRejectsEmptyWidget(t *testing.T) {
	// Ещё не разложенный виджет (AddTab до раскладки) — слать нечего.
	if w, h, ok := dispResizeTarget(0, 0, 1.0, 0, 0); ok {
		t.Errorf("пустой виджет дал запрос (%d,%d)", w, h)
	}
	if w, h, ok := dispResizeTarget(-5, 300, 1.0, 0, 0); ok {
		t.Errorf("отрицательная ширина дала запрос (%d,%d)", w, h)
	}
}
