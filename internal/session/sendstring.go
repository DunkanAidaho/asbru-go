package session

// M30: периодический 'send string' (легаси PACTerminal.pm:664-678): раз в
// N секунд в поток отдаётся SendStringTxt (+"\n" при 'send string intro'),
// только после CONNECTED и с подстановкой макросов (легаси _subst per tick).

import (
	"fmt"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// sendStringSpec — базовая строка, флаг перевода строки и интервал; ok=false —
// фича выключена (SendStringActive!=1). Intro-строка — как легаси Perl-истинность
// (используется в sendStringSpec; пустая/«0» — без перевода строки).
func sendStringSpec(cfg *config.Environment) (txt string, newline bool, every time.Duration, ok bool) {
	if cfg == nil || cfg.SendStringActive != 1 {
		return "", false, 0, false
	}
	n := int(cfg.SendStringEvery)
	if n <= 0 {
		n = 1 // guard: тик/пауза на 0 секунд
	}
	return cfg.SendStringTxt, introOn(cfg.SendStringIntro), time.Duration(n) * time.Second, true
}

// introOn — truthiness строки-флага (легаси Perl: "" и "0" — ложь).
func introOn(s string) bool {
	return s != "" && s != "0"
}

// sendStringLoop — горутина периодической отправки; живёт между реконнектами
// (как forwardInput), пишет в текущий поток ТОЛЬКО после CONNECTED (M30-ревью
// #1) и с подстановкой макросов по резолверу (M30-ревью #2, легаси _subst).
//
// Finding #9: спецификация ('send string txt/intro/every') перечитывается из
// cfg перед каждым ожиданием, а RefreshConfig будит цикл через cfgNotify —
// поэтому edit→Restart (и даже правка на лету) применяет новые значения.
func (s *Session) sendStringLoop() {
	defer func() {
		if p := recover(); p != nil {
			s.emit(Event{Kind: EventFailed, Text: fmt.Sprintf("send string panic: %v", p)})
		}
	}()
	for {
		_, _, every, ok := sendStringSpec(s.envConfig())
		if !ok {
			// фича выключена: ждём ctx или изменения cfg (tick == nil).
			if s.waitSend(nil) == sendWaitStop {
				return
			}
			continue
		}
		t := time.NewTimer(every)
		kind := s.waitSend(t.C)
		t.Stop()
		switch kind {
		case sendWaitStop:
			return
		case sendWaitCfg:
			continue // пересчитать интервал/строку по новой конфигурации
		}
		// M30-ревью #4: если отмена пришла одновременно с тиком (select выбрал
		// тик первым) — не пишем и выходим, чтобы cancel останавливал детерминированно.
		if s.ctx.Err() != nil {
			return
		}
		if !s.connected.Load() {
			continue // M30-ревью #1: не шлём до логина (не вводим в промпт)
		}
		// перечитываем на момент отправки: правка могла прийти во время тика.
		txt, newline, _, ok := sendStringSpec(s.envConfig())
		if !ok {
			continue
		}
		if st := s.getCur(); st != nil {
			payload := txt
			if newline {
				payload += "\n"
			}
			// легаси _subst per tick: нулевой резолвер — инертен (Substitute).
			payload = Substitute(payload, s.resolver())
			_, _ = st.Write([]byte(payload))
		}
	}
}

// sendWaitKind — исход ожидания send-string цикла.
type sendWaitKind int

const (
	sendWaitStop sendWaitKind = iota // ctx отменён
	sendWaitTick                     // истёк интервал
	sendWaitCfg                      // конфигурация изменилась (RefreshConfig)
)

// waitSend — ждёт тик/отмену/изменение конфигурации. tick и notify могут быть
// nil (case отключается); после срабатывания немедленно возвращает причину.
func (s *Session) waitSend(tick <-chan time.Time) sendWaitKind {
	var notify <-chan struct{}
	if s.cfgNotify != nil {
		notify = s.cfgNotify
	}
	select {
	case <-s.ctx.Done():
		return sendWaitStop
	case <-notify:
		return sendWaitCfg
	case <-tick:
		return sendWaitTick
	}
}
