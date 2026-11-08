package rdpview

// Колесо внутри RDP-сессии. В RDP вращение кодируется младшими 9 битами флагов
// указателя (FreeRDP: WheelRotationMask 0x01FF): бит 0x0100 — знак, биты 0..7 —
// величина, причём для отрицательного вращения это НЕ «знак + величина», а
// 9-битное ДОПОЛНЕНИЕ ДО ДВУХ (канон FreeRDP, client/X11/xf_client.c:1079-1083:
//   { Button4, PTR_FLAGS_WHEEL | 0x78 },
//   /* Negative value is 9bit twos complement */
//   { Button5, PTR_FLAGS_WHEEL | PTR_FLAGS_WHEEL_NEGATIVE | (0x100 - 0x78) },
// т.е. вниз на щелчок = 0x0388, а не 0x0378). Поле читается сервером как
// знаковое 9-битное: 0x188 = 392 - 512 = -120.
//
// Живой симптом «вниз работает, вверх нет» объясняется именно этим: наш старый
// код слал 0x0200 (величина 0 — сервер не крутит) и 0x0300 (поле 0x100 = 256 =
// -256 → это ДВА щелчка вниз). То есть поле — дополнение до двух, иначе не
// работало бы ни одно направление.

import "testing"

func TestWheelFlagsRotation(t *testing.T) {
	cases := []struct {
		name string
		dy   int32
		want uint16
	}{
		{"щелчок вверх", 120, 0x0278},
		{"щелчок вниз (дополнение до двух: 0x100-0x78)", -120, 0x0388},
		{"два щелчка вверх", 240, 0x02F0},
		{"два щелчка вниз (0x100-0xF0 = 0x10)", -240, 0x0310},
		{"пять щелчков вверх — не больше двух за событие", 600, 0x02F0},
		{"пять щелчков вниз — не больше двух за событие", -600, 0x0310},
		{"нет вращения", 0, 0},
	}
	for _, c := range cases {
		var acc int32
		got := wheelStep(&acc, c.dy)
		if got != c.want {
			t.Errorf("%s: dy=%d → flags=0x%04X, ожидалось 0x%04X", c.name, c.dy, got, c.want)
		}
	}
}

func TestWheelAccumulatesPartialNotches(t *testing.T) {
	var acc int32
	if f := wheelStep(&acc, 30); f != 0 {
		t.Fatalf("30 единиц — ещё не щелчок, а отправлено 0x%04X", f)
	}
	if f := wheelStep(&acc, 60); f != 0 {
		t.Fatalf("суммарно 90 — ещё не щелчок, а отправлено 0x%04X", f)
	}
	if f := wheelStep(&acc, 30); f != 0x0278 {
		t.Fatalf("суммарно 120 — ожидался щелчок 0x0278, получено 0x%04X", f)
	}
	if acc != 0 {
		t.Fatalf("после ровного щелчка остаток %d, ожидался 0", acc)
	}
}

func TestWheelKeepsRemainderAfterNotch(t *testing.T) {
	var acc int32
	if f := wheelStep(&acc, 150); f != 0x0278 {
		t.Fatalf("150 единиц — один щелчок, получено 0x%04X", f)
	}
	if acc != 30 {
		t.Fatalf("остаток после щелчка %d, ожидалось 30", acc)
	}
}

func TestWheelDirectionBySign(t *testing.T) {
	var down, up int32
	if f := wheelStep(&down, -240); f != 0x0310 {
		t.Errorf("вниз: flags=0x%04X, ожидалось 0x0310 (знак + дополнение до двух)", f)
	}
	if f := wheelStep(&up, 240); f != 0x02F0 {
		t.Errorf("вверх: flags=0x%04X, ожидалось 0x02F0 (знак + величина)", f)
	}
}

// Излишек от клампа не выбрасывается: он остаётся в накопителе и уходит
// следующими событиями (канон FreeRDP разбивает величину на несколько событий,
// а не срезает её).
func TestWheelSplitByClampKeepsExcess(t *testing.T) {
	var acc int32
	if f := wheelStep(&acc, -600); f != 0x0310 {
		t.Fatalf("первый шаг: flags=0x%04X, ожидалось 0x0310", f)
	}
	if acc != -360 {
		t.Fatalf("остаток %d, ожидалось -360 (пять щелчков минус два отправленных)", acc)
	}
	if f := wheelStep(&acc, 0); f != 0x0310 {
		t.Fatalf("второй шаг: flags=0x%04X, ожидалось 0x0310", f)
	}
	if f := wheelStep(&acc, 0); f != 0x0388 {
		t.Fatalf("третий шаг: flags=0x%04X, ожидалось 0x0388", f)
	}
	if acc != 0 || wheelStep(&acc, 0) != 0 {
		t.Fatalf("после отправки всего вращения накопитель должен быть пуст (остаток %d)", acc)
	}
}

// M40-остаток: горизонтальное колесо — PTR_FLAGS_HWHEEL с той же 9-битной
// величиной (канон FreeRDP client/X11/xf_client.c:1091:
// { Button6, PTR_FLAGS_HWHEEL | 0x78 }, { Button7, PTR_FLAGS_HWHEEL |
// PTR_FLAGS_WHEEL_NEGATIVE | (0x100 - 0x78) } — ВНИЗ по X положительное).
func TestHWheelFlags(t *testing.T) {
	cases := []struct {
		name string
		dx   int32
		want uint16
	}{
		{"вправо (вперёд)", 120, 0x0478}, // HWHEEL | 0x78
		{"влево (назад)", -120, 0x0588},  // HWHEEL|NEG | (0x100-0x78)
		{"дробь не щелчок", 30, 0x0000},
	}
	var acc int32
	for _, c := range cases {
		if got := hwheelStep(&acc, c.dx); got != c.want {
			t.Errorf("%s: dx=%d → 0x%04X, ожидалось 0x%04X", c.name, c.dx, got, c.want)
		}
	}
}
