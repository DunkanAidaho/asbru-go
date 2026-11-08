package session

import (
	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// Промпты подключения — алиасы дефолтов config (легаси PACUtils.pm:234-239);
// сид дефолтов выполняется санитизацией, сюда они попадают через WithPrompts.
const (
	DefaultCommandPrompt        = config.DefaultCommandPrompt
	DefaultUsernamePrompt       = config.DefaultUsernamePrompt
	DefaultPasswordPrompt       = config.DefaultPasswordPrompt
	DefaultHostkeyChangedPrompt = config.DefaultHostkeyChangedPrompt
	DefaultPressAnyKeyPrompt    = config.DefaultPressAnyKeyPrompt
	DefaultRemoteHostPrompt     = config.DefaultRemoteHostPrompt
	DefaultSudoPrompt           = config.DefaultSudoPrompt
)

// Prompts — регэкспы/литералы промптов рукопожатия + авто-приём ключа.
type Prompts struct {
	Command           string // command prompt (завершение рукопожатия)
	Username          string // login prompt
	Password          string // password prompt
	HostkeyChanged    string // "(... continue connecting (yes/no)?"
	AnyKey            string // "Press any key to continue"
	RemoteHostChanged string // "Offending key in <file>:<line>"
	SudoPrompt        string // клиентский sudo -p (литерал)
	SudoPassword      string // '<<ASK_PASS>>' или статичный
	AutoAcceptKey     bool   // 'auto accept key'
}

// fill — дефолты из config (запуск без настроек должен работать сразу).
func (p Prompts) fill() Prompts {
	if p.Command == "" {
		p.Command = DefaultCommandPrompt
	}
	if p.Username == "" {
		p.Username = DefaultUsernamePrompt
	}
	if p.Password == "" {
		p.Password = DefaultPasswordPrompt
	}
	if p.HostkeyChanged == "" {
		p.HostkeyChanged = DefaultHostkeyChangedPrompt
	}
	if p.AnyKey == "" {
		p.AnyKey = DefaultPressAnyKeyPrompt
	}
	if p.RemoteHostChanged == "" {
		p.RemoteHostChanged = DefaultRemoteHostPrompt
	}
	if p.SudoPrompt == "" {
		p.SudoPrompt = DefaultSudoPrompt
	}
	return p
}
