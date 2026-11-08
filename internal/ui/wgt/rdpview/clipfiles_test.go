package rdpview

// Файлы в буфере обмена RDP (M39, направление локальный→сервер). На проводе
// клиент рекламирует FileGroupDescriptorW, а его данные — упакованный список
// файлов MS-RDPECLIP 2.2.5.2.3 (CLIPRDR_FILELIST): UINT32 cItems +
// cItems × FILEDESCRIPTORW. (CF_HDROP/15 — локальная Win32-форма DROPFILES,
// на провод не идёт.) Содержимое
// файлов сервер затем запрашивает через FILECONTENTS_REQUEST, которое отдаёт
// cliprdr_file_context (клиентский поток), поэтому порядок дескрипторов обязан
// совпадать с порядком обхода URI-списка в FreeRDP (DFS, дети в порядке readdir).
//
// Qt-обвязка (QClipboard/QMimeData, cliprdr_file_context_init) проверяется
// только cgo-сборкой и трассировкой кода: C++-стенда и живого RDP-сервера нет.

import (
	"bytes"
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unicode/utf16"
)

// --- text/uri-list ---

func TestURIListBuildSingleFile(t *testing.T) {
	out, ok := uriListBuild([]string{"/tmp/a b.txt"})
	if !ok {
		t.Fatal("uriListBuild отверг валидный путь")
	}
	want := "file:///tmp/a%20b.txt\r\n"
	if string(out) != want {
		t.Fatalf("uri-list = %q, ожидалось %q", out, want)
	}
}

func TestURIListBuildMultipleFiles(t *testing.T) {
	out, ok := uriListBuild([]string{"/tmp/a", "/tmp/b c", "/tmp/d"})
	if !ok {
		t.Fatal("uriListBuild отверг валидные пути")
	}
	want := "file:///tmp/a\r\nfile:///tmp/b%20c\r\nfile:///tmp/d\r\n"
	if string(out) != want {
		t.Fatalf("uri-list = %q, ожидалось %q", out, want)
	}
}

// URL-кодирование: пробел, UTF-8 (кириллица и emoji) и зарезервированные
// символы. Каждый БАЙТ UTF-8 кодируется отдельно, как требует RFC 3986.
func TestURIListBuildUTF8AndReserved(t *testing.T) {
	p := "/tmp/Привет 🌍/#1?%.txt"
	out, ok := uriListBuild([]string{p})
	if !ok {
		t.Fatal("uriListBuild отверг UTF-8 путь")
	}
	want := "file:///tmp/" +
		"%D0%9F%D1%80%D0%B8%D0%B2%D0%B5%D1%82" + // Привет
		"%20" +
		"%F0%9F%8C%8D" + // 🌍
		"/%231%3F%25.txt\r\n"
	if string(out) != want {
		t.Fatalf("uri-list = %q, ожидалось %q", out, want)
	}
	// Незарезервированные символы не кодируются.
	out, ok = uriListBuild([]string{"/tmp/-._~abz"})
	if !ok || string(out) != "file:///tmp/-._~abz\r\n" {
		t.Fatalf("unreserved = %q (ok=%v)", out, ok)
	}
}

func TestURIListBuildDirectory(t *testing.T) {
	out, ok := uriListBuild([]string{"/home/user/dir"})
	if !ok || string(out) != "file:///home/user/dir\r\n" {
		t.Fatalf("каталог = %q (ok=%v)", out, ok)
	}
}

func TestURIListBuildEmpty(t *testing.T) {
	for _, paths := range [][]string{nil, {}} {
		if out, ok := uriListBuild(paths); ok || out != nil {
			t.Fatalf("пустой список принят: %q", out)
		}
	}
}

func TestURIListBuildSkipsEmptyPath(t *testing.T) {
	out, ok := uriListBuild([]string{"", "/tmp/x", ""})
	if !ok || string(out) != "file:///tmp/x\r\n" {
		t.Fatalf("пустые элементы не пропущены: %q (ok=%v)", out, ok)
	}
}

// ag-q2t: корневой FIFO (и unix-сокет) в uri-список попадать не должен.
// cliprdr_file_context_update_client_data создаёт по нему запись, и позже
// winpr_fopen(FIFO, "rb") блокируется навсегда (воркер «мёртв»). Фильтр обязан
// быть согласован с CLIPRDR_FILELIST — иначе listIndex дескрипторов разойдётся
// с записями file_context.
func mkfifoForTest(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo недоступен: %v", err)
	}
}

func TestURIListBuildSkipsRootFIFO(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	mkfifoForTest(t, fifo)
	if out, ok := uriListBuild([]string{fifo}); ok || out != nil {
		t.Fatalf("FIFO попал в uri-list: %q", out)
	}
}

func TestURIListBuildSkipsRootFIFOAmongFiles(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	writeFile(t, a, []byte("a"))
	fifo := filepath.Join(dir, "pipe")
	mkfifoForTest(t, fifo)
	b := filepath.Join(dir, "b")
	writeFile(t, b, []byte("b"))

	out, ok := uriListBuild([]string{a, fifo, b})
	if !ok {
		t.Fatal("uriListBuild отверг список с обычными файлами")
	}
	if string(out) != "file://"+a+"\r\nfile://"+b+"\r\n" {
		t.Fatalf("uri-list = %q", out)
	}
}

func TestURIListBuildSkipsRootSocket(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("unix-сокет недоступен: %v", err)
	}
	defer ln.Close()
	if out, ok := uriListBuild([]string{sock}); ok || out != nil {
		t.Fatalf("сокет попал в uri-list: %q", out)
	}
}

// --- FileGroupDescriptorW (CLIPRDR_FILELIST) ---

const fileListDescriptorLen = 592 // FILEDESCRIPTORW на проводе

// fileListEntry — разобранный FILEDESCRIPTORW для проверок.
type fileListEntry struct {
	flags   uint32
	attrs   uint32
	size    uint64
	name    string
	isDir   bool
	lastMod uint64
}

func parseFileList(t *testing.T, b []byte) []fileListEntry {
	t.Helper()
	if len(b) < 4 {
		t.Fatalf("payload короче заголовка: %d", len(b))
	}
	count := binary.LittleEndian.Uint32(b[:4])
	if int(count) != (len(b)-4)/fileListDescriptorLen {
		t.Fatalf("cItems=%d не соответствует длине %d", count, len(b))
	}
	out := make([]fileListEntry, 0, count)
	for i := uint32(0); i < count; i++ {
		d := b[4+int(i)*fileListDescriptorLen:]
		e := fileListEntry{
			flags: binary.LittleEndian.Uint32(d[0:]),
			attrs: binary.LittleEndian.Uint32(d[36:]),
			// nFileSizeHigh по смещению 64, nFileSizeLow — 68.
			size: uint64(binary.LittleEndian.Uint32(d[64:]))<<32 |
				uint64(binary.LittleEndian.Uint32(d[68:])),
			lastMod: binary.LittleEndian.Uint64(d[56:]),
			isDir:   binary.LittleEndian.Uint32(d[36:])&0x10 != 0,
		}
		units := make([]uint16, 0, 260)
		for j := 0; j < 260; j++ {
			u := binary.LittleEndian.Uint16(d[72+j*2:])
			if u == 0 {
				break
			}
			units = append(units, u)
		}
		e.name = string(utf16.Decode(units))
		out = append(out, e)
	}
	return out
}

func fileListNames(entries []fileListEntry) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.name
	}
	return names
}

func mustFileList(t *testing.T, paths []string) []fileListEntry {
	t.Helper()
	out, ok := cliprdrFileListBuild(paths)
	if !ok {
		t.Fatalf("cliprdrFileListBuild отверг %v", paths)
	}
	return parseFileList(t, out)
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCliprdrFileListSingleFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "hello world.txt")
	writeFile(t, p, []byte("abc"))

	entries := mustFileList(t, []string{p})
	if len(entries) != 1 {
		t.Fatalf("записей = %d, ожидалось 1", len(entries))
	}
	e := entries[0]
	if e.name != "hello world.txt" {
		t.Fatalf("имя = %q", e.name)
	}
	if e.isDir {
		t.Fatal("файл помечен каталогом")
	}
	if e.attrs&0x80 == 0 { // FILE_ATTRIBUTE_NORMAL
		t.Fatalf("attrs = %#x, нет FILE_ATTRIBUTE_NORMAL", e.attrs)
	}
	if e.size != 3 {
		t.Fatalf("размер = %d, ожидалось 3", e.size)
	}
	// FD_ATTRIBUTES|FD_FILESIZE|FD_WRITESTIME|FD_PROGRESSUI|FD_CREATETIME|FD_ACCESSTIME
	const wantFlags = 0x04 | 0x40 | 0x20 | 0x4000 | 0x08 | 0x10
	if e.flags&wantFlags != wantFlags {
		t.Fatalf("flags = %#x, ожидались биты %#x", e.flags, wantFlags)
	}
	if e.lastMod == 0 {
		t.Fatal("ftLastWriteTime = 0")
	}
}

func TestCliprdrFileListMultipleFilesPreserveOrder(t *testing.T) {
	dir := t.TempDir()
	paths := []string{
		filepath.Join(dir, "one"),
		filepath.Join(dir, "two"),
		filepath.Join(dir, "three"),
	}
	for i, p := range paths {
		writeFile(t, p, []byte(strings.Repeat("x", i+1)))
	}
	entries := mustFileList(t, paths)
	got := fileListNames(entries)
	want := []string{"one", "two", "three"}
	if len(got) != len(want) {
		t.Fatalf("имена = %v, ожидалось %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("имена = %v, ожидалось %v", got, want)
		}
		if entries[i].size != uint64(i+1) {
			t.Fatalf("размер %q = %d", got[i], entries[i].size)
		}
	}
}

// Ожидаемый порядок обхода повторяет cliprdr_local_stream_update:
// pre-order DFS, дети каталога — в порядке readdir (Readdirnames), FIFO
// пропускаются. Сравниваем именно последовательность, от неё зависит
// listIndex в FILECONTENTS_REQUEST.
func expectedFileListNames(t *testing.T, paths []string) []string {
	t.Helper()
	var out []string
	var walk func(p, rel string)
	walk = func(p, rel string) {
		out = append(out, rel)
		fi, err := os.Stat(p)
		if err != nil || !fi.IsDir() {
			return
		}
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		names, err := f.Readdirnames(-1)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			// Как wf_cliprdr_process_filename: потомки — путь относительно
			// родителя корня, разделитель Windows.
			next := name
			if rel != "" {
				next = rel + "\\" + name
			}
			walk(filepath.Join(p, name), next)
		}
	}
	for _, p := range paths {
		walk(p, filepath.Base(p))
	}
	return out
}

func TestCliprdrFileListDirectoryRecursiveOrder(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), []byte("a"))
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sub, "nested.txt"), []byte("nested"))
	writeFile(t, filepath.Join(root, "b.txt"), []byte("bb"))

	entries := mustFileList(t, []string{root})
	got := fileListNames(entries)
	want := expectedFileListNames(t, []string{root})
	if len(got) != len(want) {
		t.Fatalf("имена = %v, ожидалось %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("имена = %v, ожидалось %v", got, want)
		}
	}
	if !entries[0].isDir {
		t.Fatal("корень не помечен каталогом")
	}
	// Вложенный файл присутствует как путь относительно корня.
	base := filepath.Base(root)
	wantNested := base + "\\sub\\nested.txt"
	found := false
	for _, n := range got {
		if n == wantNested {
			found = true
		}
	}
	if !found {
		t.Fatalf("%q потерян: %v", wantNested, got)
	}
}

func TestCliprdrFileListEmptyDirectory(t *testing.T) {
	root := t.TempDir()
	entries := mustFileList(t, []string{root})
	if len(entries) != 1 {
		t.Fatalf("записей = %d, ожидалась 1 (сам каталог)", len(entries))
	}
	if !entries[0].isDir || entries[0].name != filepath.Base(root) {
		t.Fatalf("каталог = %+v", entries[0])
	}
	if entries[0].size != 0 {
		t.Fatalf("размер каталога = %d, ожидалось 0", entries[0].size)
	}
}

func TestCliprdrFileListUTF8Filename(t *testing.T) {
	dir := t.TempDir()
	name := "файл 🌍.txt"
	p := filepath.Join(dir, name)
	writeFile(t, p, []byte("u"))

	entries := mustFileList(t, []string{p})
	if len(entries) != 1 || entries[0].name != name {
		t.Fatalf("имя = %q, ожидалось %q", entries[0].name, name)
	}
}

func TestCliprdrFileListMissingPathStillListed(t *testing.T) {
	// file_context добавляет запись URI-списка даже если путь не существует
	// (append_entry без проверки); содержимое запросится позже и провалится.
	entries := mustFileList(t, []string{"/nonexistent/definitely-missing"})
	if len(entries) != 1 || entries[0].name != "definitely-missing" {
		t.Fatalf("entries = %+v", entries)
	}
}

// ag-73o: winpr перед отправкой проверяет каждый компонент имени через
// ValidFileNameComponent (winpr/libwinpr/file/file.c) и при невалидном имени
// роняет конверсию ВСЕГО списка — эталонный клиент такой файл не отправляет
// («такие имена роняют некоторые оболочки Windows при вставке»). Повторяем:
// список не строится, FileGroupDescriptorW не объявляется.
func TestCliprdrFileListInvalidWindowsNameRejected(t *testing.T) {
	invalid := []string{
		// запрещённые символы (кроме '/', который в компоненте пути не встречается)
		"a:b.txt", "a<b", "a>b", `a"b`, "a|b", "a?b", "a*b", `a\b`,
		// зарезервированные имена DOS-устройств (ровно 3 или 4 символа)
		"CON", "con", "PrN", "AUX", "NUL",
		"COM0", "COM9", "LPT1", "lpt9",
	}
	for _, name := range invalid {
		dir := t.TempDir()
		p := filepath.Join(dir, name)
		writeFile(t, p, nil)
		if out, ok := cliprdrFileListBuild([]string{p}); ok || out != nil {
			t.Errorf("%q: список отправлен (%d байт), ожидался отказ", name, len(out))
		}
	}
}

// Невалидное имя ВНУТРИ каталога тоже роняет весь список (winpr:
// add_directory_entry_to_list → FALSE → update_client_data не публикует ни один
// файл), а не пропускает только плохую запись.
func TestCliprdrFileListInvalidChildNameRejectsWholeList(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "ok.txt"), []byte("x"))
	writeFile(t, filepath.Join(root, "bad:name.txt"), nil)
	if out, ok := cliprdrFileListBuild([]string{root}); ok || out != nil {
		t.Fatalf("список с невалидным именем отправлен: %d байт", len(out))
	}
}

// Невалидное имя КОРНЕВОГО элемента (каталога) — тоже отказ.
func TestCliprdrFileListInvalidRootDirectoryRejected(t *testing.T) {
	root := filepath.Join(t.TempDir(), "a:b")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "ok"), nil)
	if out, ok := cliprdrFileListBuild([]string{root}); ok || out != nil {
		t.Fatalf("список с невалидным корнем отправлен: %d байт", len(out))
	}
}

// Имена, которые winpr ПРИНИМАЕТ, не должны отсекаться: проверка зарезервированных
// имён — точное совпадение (CON.txt/COM проходят, LPT10 — нет в списке 0..9), а
// завершающие точку/пробел winpr не проверяет вовсе. Паритет с эталоном важнее
// строгости Windows — иначе файл, который xfreerdp отправляет, у нас бы пропадал.
func TestCliprdrFileListWindowsValidNamesAccepted(t *testing.T) {
	valid := []string{
		"CONSOLE", "CON.txt", "COM", "LPT10", "aux1.txt",
		"hello world.txt", "файл 🌍.txt", "a-b_c.txt", "trailing.",
	}
	for _, name := range valid {
		dir := t.TempDir()
		p := filepath.Join(dir, name)
		writeFile(t, p, nil)
		entries := mustFileList(t, []string{p})
		if len(entries) != 1 || entries[0].name != name {
			t.Errorf("%q: entries = %+v", name, entries)
		}
	}
}

// ag-q2t: корневой FIFO не должен попадать в FileGroupDescriptorW. winpr
// FindNextFile пропускает FIFO у детей, а для корня make_synthetic_file вообще
// не создаёт handle — эталонный клиент тогда список не шлёт. Наш обход раньше
// писал FIFO как обычный файл (attrs NORMAL, size 0); сервер запрашивал
// содержимое, и winpr_fopen(FIFO, "rb") вешал воркер до появления писателя.
func TestCliprdrFileListRootFIFOSkipped(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	mkfifoForTest(t, fifo)

	// Единственный путь — FIFO: списка нет вовсе (как у эталона).
	if out, ok := cliprdrFileListBuild([]string{fifo}); ok || out != nil {
		t.Fatalf("FIFO попал в список: %d байт (%+v)", len(out), parseFileList(t, out))
	}
}

func TestCliprdrFileListRootFIFOSkippedAmongFiles(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	writeFile(t, a, []byte("a"))
	fifo := filepath.Join(dir, "pipe")
	mkfifoForTest(t, fifo)
	b := filepath.Join(dir, "b")
	writeFile(t, b, []byte("b"))

	entries := mustFileList(t, []string{a, fifo, b})
	got := fileListNames(entries)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("имена = %v, ожидалось [a b]", got)
	}
}

func TestCliprdrFileListRootSocketSkipped(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("unix-сокет недоступен: %v", err)
	}
	defer ln.Close()

	if out, ok := cliprdrFileListBuild([]string{sock}); ok || out != nil {
		t.Fatalf("сокет попал в список: %d байт", len(out))
	}
}

// FIFO среди ДЕТЕЙ каталога пропускается и раньше (winpr FindNextFile тоже);
// тест закрепляет это рядом с корневым случаем.
func TestCliprdrFileListChildFIFOSkipped(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), []byte("a"))
	mkfifoForTest(t, filepath.Join(root, "pipe"))

	entries := mustFileList(t, []string{root})
	got := fileListNames(entries)
	base := filepath.Base(root)
	want := []string{base, base + "\\a.txt"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("имена = %v, ожидалось %v", got, want)
	}
}

func TestCliprdrFileListEmpty(t *testing.T) {
	for _, paths := range [][]string{nil, {}} {
		if out, ok := cliprdrFileListBuild(paths); ok || out != nil {
			t.Fatalf("пустой список принят: %d байт", len(out))
		}
	}
}

// --- Пределы сборки файлового списка (ag-32m) ---
// Копирование каталога с огромным числом файлов не должно подвешивать UI и
// раздувать память: обход бросается при превышении лимита по числу записей,
// байтам полезной нагрузки или времени. Ноль в любом поле = «без предела».

func TestCliprdrFileListItemLimitRejects(t *testing.T) {
	dir := t.TempDir()
	paths := make([]string, 0, 4)
	for i := 0; i < 4; i++ {
		p := filepath.Join(dir, string(rune('a'+i)))
		writeFile(t, p, nil)
		paths = append(paths, p)
	}
	if out, ok := cliprdrFileListBuildLimits(paths, 2, 0, 0); ok || out != nil {
		t.Fatalf("список из 4 записей принят при пределе 2: %d байт", len(out))
	}
}

func TestCliprdrFileListItemLimitBoundary(t *testing.T) {
	dir := t.TempDir()
	paths := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		p := filepath.Join(dir, string(rune('a'+i)))
		writeFile(t, p, nil)
		paths = append(paths, p)
	}
	out, ok := cliprdrFileListBuildLimits(paths, 2, 0, 0)
	if !ok {
		t.Fatal("список ровно на пределе отвергнут")
	}
	if entries := parseFileList(t, out); len(entries) != 2 {
		t.Fatalf("записей = %d, ожидалось 2", len(entries))
	}
}

func TestCliprdrFileListByteLimitRejects(t *testing.T) {
	dir := t.TempDir()
	paths := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		p := filepath.Join(dir, string(rune('a'+i)))
		writeFile(t, p, nil)
		paths = append(paths, p)
	}
	// Один дескриптор (592) + заголовок (4) помещается; два — уже нет.
	if out, ok := cliprdrFileListBuildLimits(paths, 0, 4+fileListDescriptorLen, 0); ok || out != nil {
		t.Fatalf("список из 2 записей принят при байтовом пределе на одну: %d байт", len(out))
	}
	out, ok := cliprdrFileListBuildLimits(paths, 0, 4+2*fileListDescriptorLen, 0)
	if !ok {
		t.Fatal("список ровно в байтовый предел отвергнут")
	}
	if entries := parseFileList(t, out); len(entries) != 2 {
		t.Fatalf("записей = %d, ожидалось 2", len(entries))
	}
}

// Заголовок CLIPRDR_FILELIST — little-endian cItems; дескриптор — 592 байта.
func TestCliprdrFileListWireLayout(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x")
	writeFile(t, p, []byte("12345"))
	out, ok := cliprdrFileListBuild([]string{p})
	if !ok {
		t.Fatal("cliprdrFileListBuild отверг файл")
	}
	if !bytes.Equal(out[:4], []byte{1, 0, 0, 0}) {
		t.Fatalf("cItems = % x", out[:4])
	}
	if len(out) != 4+fileListDescriptorLen {
		t.Fatalf("длина = %d, ожидалось %d", len(out), 4+fileListDescriptorLen)
	}
}
