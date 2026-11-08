package config

// Дефолтные промпты подключения (легаси PACUtils.pm:234-239); сеются в
// defaults при санитизации и используются сессией через Prompts.
const (
	DefaultCommandPrompt        = `([#%:>~\$\] ]){3}|(\w[@\/]\w|sftp).*?[#%>~\$\]]|([\w\-\.]+)[%>\$\]]( |\033)|^[#%\$>\:\]~] *$`
	DefaultUsernamePrompt       = `([lL]ogin|[uU]suario|([uU]ser-?)*[nN]ame.*|[uU]ser)\s*:\s*$`
	DefaultPasswordPrompt       = `([pP]ass|[pP]ass[wW]or[dt](\s+for\s+|\w+@[\w\-\.]+)*|[cC]ontrase.a|Enter passphrase for key '.+')\s*:\s*$`
	DefaultHostkeyChangedPrompt = `^.+ontinue connecting \(([^/]+)\/([^/]+)(?:[^)]+)?\)\?\s*$`
	DefaultPressAnyKeyPrompt    = `.*(any key to continue|tecla para continuar).*`
	DefaultRemoteHostPrompt     = `.*ffending .*key in (.+?)\:(\d+).*`
	DefaultSudoPrompt           = `[__PAC__SUDO__PROMPT__]`
)
