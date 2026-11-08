# Асбру (Go-порт): цель локализации (i18n), аудит #4 / KDE Tier-1.
#
# Схема: `go run ./cmd/asbru-i18n` сканирует internal/*.go на i18n.Tr(...) и
# пишет Qt Linguist .ts; `lrelease6` компилирует его в .qm, который поднимает
# installTranslator() (internal/ui) по $LANG. Locale по умолчанию — ru.
#
# Использование:
#   make i18n        # i18n/asbru_ru.ts + i18n/asbru_ru.qm
#   make i18n-ts     # только .ts
#   make i18n-check  # .ts в актуальном состоянии (diff регенерация vs коммит)

LOCALE   ?= ru
I18N_DIR ?= i18n
GEN      := go run ./cmd/asbru-i18n
LRELEASE ?= lrelease6

.PHONY: i18n i18n-ts i18n-qm i18n-check clean-i18n

i18n-ts:
	$(GEN) -root . -lang $(LOCALE) -out $(I18N_DIR)/asbru_$(LOCALE).ts

i18n-qm: i18n-ts
	$(LRELEASE) $(I18N_DIR)/asbru_$(LOCALE).ts -qm $(I18N_DIR)/asbru_$(LOCALE).qm

i18n: i18n-qm

# Проверка каталога: все извлечённые (context, source) есть в коммитнутом .ts
# (без учёта тел переводов — заполненные переводы не ломают проверку).
i18n-check:
	$(GEN) -root . -lang $(LOCALE) -check $(I18N_DIR)/asbru_$(LOCALE).ts

clean-i18n:
	rm -f $(I18N_DIR)/asbru_*.ts $(I18N_DIR)/asbru_*.qm
