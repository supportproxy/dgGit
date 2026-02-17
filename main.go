package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time" // Used for safety pauses during setup

	"github.com/atotto/clipboard"
	"github.com/gen2brain/dlgs"
	"github.com/sqweek/dialog"
	"golang.org/x/sys/windows/registry"
)

// --- CONSTANTS ---
const (
	AppName        = "dgGit"
	ConfigFileName = "dggit.cfg"
)

// --- CONFIG STRUCT ---
type Config struct {
	StartDir      string
	Extension     string
	PrefixToStrip string
	ShowSuccess   bool
	AutoSave      bool
	GitAutoCommit bool
}

// -- MY NOTES --
// go build -ldflags -H=windowsgui -o dgGit.exe

func main() {
	// 1. Load Config
	cfg, cancelled, isFirstRun := loadConfig()
	if cancelled {
		return
	}

	// 2. Registry Maintenance
	if err := updateRegistry(); err != nil {
		dlgs.Error(AppName, fmt.Sprintf("Setup Error:\nFailed to update registry settings.\n%s", err))
		return
	}

	if isFirstRun {
		return
	}

	// 3. Determine Execution Mode
	var saveDir string

	if len(os.Args) > 1 {
		// MODE A: User right-clicked a Folder Icon
		saveDir = strings.TrimSpace(os.Args[1])
	} else {
		// MODE B: User right-clicked Background
		if cfg.AutoSave {
			if _, err := os.Stat(cfg.StartDir); err == nil {
				saveDir = cfg.StartDir
			} else {
				saveDir = askForFolder(cfg.StartDir)
			}
		} else {
			saveDir = askForFolder(cfg.StartDir)
		}
	}

	if saveDir == "" {
		return
	}

	// 4. Get Clipboard
	content, err := clipboard.ReadAll()
	if err != nil || content == "" {
		return
	}

	// 5. Parse & Sanitize
	lines := strings.Split(content, "\n")
	firstLine := strings.TrimSpace(strings.ReplaceAll(lines[0], "\r", ""))
	if firstLine == "" {
		return
	}

	// --- UPDATED: HANDLE MULTIPLE PREFIXES (SPLIT BY PIPE) ---
	filenameRaw := firstLine
	if cfg.PrefixToStrip != "" {
		// Split config by pipe "|" to get all options
		prefixes := strings.Split(cfg.PrefixToStrip, "|")
		for _, p := range prefixes {
			// Check if this specific prefix matches the start of the line
			if p != "" && strings.HasPrefix(firstLine, p) {
				filenameRaw = strings.TrimPrefix(firstLine, p)
				break // Stop after finding the first match
			}
		}
	}

	safeFilename := sanitizeFilename(strings.TrimSpace(filenameRaw)) + cfg.Extension
	fullPath := filepath.Join(saveDir, safeFilename)

	// 6. Save File
	err = os.WriteFile(fullPath, []byte(content), 0644)
	if err != nil {
		dlgs.Error(AppName, fmt.Sprintf("Error saving file:\n%s", err))
		return
	}

	// 7. Git Auto-Commit
	var gitMessage string
	if cfg.GitAutoCommit {
		if err := runGitCommit(saveDir, safeFilename); err != nil {
			gitMessage = fmt.Sprintf("\n(Git Commit Failed: %s)", err)
		} else {
			gitMessage = "\n(Git Commit Successful)"
		}
	}

	// 8. Clear Clipboard
	_ = clipboard.WriteAll("")

	// 9. Success Message
	if cfg.ShowSuccess {
		dlgs.Info(AppName, fmt.Sprintf("Saved to:\n%s%s\n\n(Clipboard Cleared)", fullPath, gitMessage))
	}
}

// --- SETUP WIZARD & CONFIG ---

func loadConfig() (Config, bool, bool) {
	path := filepath.Join(getExeDir(), ConfigFileName)

	if _, err := os.Stat(path); os.IsNotExist(err) {
		cfg, cancelled := runSetupWizard(path)
		return cfg, cancelled, true
	}

	cfg := Config{
		StartDir:      ".",
		Extension:     ".dg",
		PrefixToStrip: "void ",
		ShowSuccess:   true,
		AutoSave:      false,
		GitAutoCommit: false,
	}

	file, err := os.Open(path)
	if err != nil {
		return cfg, false, false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, val := strings.ToLower(strings.TrimSpace(parts[0])), strings.TrimSpace(parts[1])

		switch key {
		case "startdir":
			cfg.StartDir = val
		case "extension":
			cfg.Extension = val
		case "prefixtostrip":
			// Allow reading the raw string including pipes
			cfg.PrefixToStrip = strings.Trim(val, "\"")
		case "showsuccessmessage":
			cfg.ShowSuccess = (val == "true")
		case "autosave":
			cfg.AutoSave = (val == "true")
		case "gitautocommit":
			cfg.GitAutoCommit = (val == "true")
		}
	}
	return cfg, false, false
}

func runSetupWizard(configPath string) (Config, bool) {
	dlgs.Info(AppName, "Welcome to dgGit!\n\nIt looks like this is your first run. This small utility is designed to save code copied to your clipboard to a local Git repo.\n\n But first, we need to set up a few preferences.")

	// --- STEP 1 ---
	dlgs.Info("Step 1 Instructions", "Next, please select the folder where you want your code saved.\n\nThe program will prompt for a save location every time and the filebrowser will start in this folder by default, so it's good to set it to your repo or a parent folder of your repo (you can change this later by editing the config file or deleting the config file and re-running the setup wizard).\n\nIf you want to use the right-click menu on a specific folder, it doesn't matter what you choose here because it will save the code from your clipboard in the folder you right-clicked on instead.")

	time.Sleep(500 * time.Millisecond)

	startDir, err := dialog.Directory().Title("Step 1: Select a folder in which to save the code copied to your clipboard (local repo?)").Browse()
	if err != nil {
		dlgs.Warning(AppName, "Setup was cancelled.")
		return Config{}, true
	}

	// --- STEP 2 ---
	time.Sleep(500 * time.Millisecond)
	dlgs.Info("Step 2 Instructions", "Next, enter the file extension you want to use for saved files.\n(e.g., .dg, .txt, .js)")

	time.Sleep(500 * time.Millisecond)
	ext, success, _ := dlgs.Entry("Step 2: File Extension", "File Extension:", ".dg")
	if !success {
		dlgs.Warning(AppName, "Setup was cancelled.")
		return Config{}, true
	}

	// --- STEP 3 (UPDATED TEXT) ---
	time.Sleep(500 * time.Millisecond)
	dlgs.Info("Step 3 Instructions", "Next, enter the text to strip from the start of your copied code to create the filename.\n\nYou can enter multiple options separated by a pipe symbol (|).\n\nExample: 'void |int |string '\n(Note the space after each word if needed)")

	time.Sleep(500 * time.Millisecond)
	prefix, success, _ := dlgs.Entry("Step 3: Prefix Stripping", "Prefixes (separate with | ):", "void |int |string ")
	if !success {
		dlgs.Warning(AppName, "Setup was cancelled.")
		return Config{}, true
	}

	// --- STEP 4 ---
	time.Sleep(500 * time.Millisecond)
	useGit, _ := dlgs.Question("Step 4: Git Integration", "Do you want to run 'git add/commit'\nautomatically upon save?", false)

	// --- STEP 5 ---
	time.Sleep(500 * time.Millisecond)
	autoSave, _ := dlgs.Question("Step 5: Auto-Save Mode", "Do you want to skip the folder dialog and\nALWAYS save to your default folder automatically?", false)

	// --- STEP 6 ---
	time.Sleep(500 * time.Millisecond)
	createShortcut, _ := dlgs.Question("Step 6: Desktop Shortcut", "Create a shortcut on your Desktop?", true)

	newCfg := Config{
		StartDir:      startDir,
		Extension:     ext,
		PrefixToStrip: prefix,
		ShowSuccess:   true,
		AutoSave:      autoSave,
		GitAutoCommit: useGit,
	}

	saveConfigToFile(configPath, newCfg)

	if createShortcut {
		if err := createDesktopShortcut(); err != nil {
			dlgs.Error(AppName, fmt.Sprintf("Could not create shortcut:\n%s", err))
		}
	}

	finalMsg := fmt.Sprintf("Setup Complete!\n\nSettings saved to:\n%s\n\n(Edit this file to change settings later)\n\nYou can now use the right-click menu or desktop shortcut.", configPath)
	dlgs.Info(AppName, finalMsg)

	return newCfg, false
}

func saveConfigToFile(path string, cfg Config) {
	content := fmt.Sprintf(`# dgGit Configuration File

# 1. Default Save Directory
# The folder where files will save (unless you right-click a specific folder).
StartDir=%s

# 2. File Extension
# The extension appended to saved files (e.g. .dg, .txt, .js)
Extension=%s

# 3. Clipboard Cleaning
# Text to automatically remove from the start of the filename.
# You can separate multiple options with a pipe symbol "|".
# Example: "void |int |string "
PrefixToStrip=%s

# 4. User Interface
# Show a popup message when a file is saved successfully? (true/false)
ShowSuccessMessage=%t

# 5. Automation
# Save immediately to StartDir without showing the folder browser? (true/false)
AutoSave=%t

# 6. Version Control
# Automatically run 'git add' and 'git commit' after saving? (true/false)
# Note: Git must be installed and the target folder must be a git repo.
GitAutoCommit=%t
`, cfg.StartDir, cfg.Extension, cfg.PrefixToStrip, cfg.ShowSuccess, cfg.AutoSave, cfg.GitAutoCommit)

	os.WriteFile(path, []byte(content), 0644)
}

func createDesktopShortcut() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	desktopPath := filepath.Join(home, "Desktop", AppName+".lnk")

	psScript := fmt.Sprintf("$s=(New-Object -COM WScript.Shell).CreateShortcut('%s');$s.TargetPath='%s';$s.Save()", desktopPath, exePath)

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript)
	return cmd.Run()
}

// --- HELPERS ---

func runGitCommit(dir, filename string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git not found")
	}
	cmdAdd := exec.Command("git", "add", filename)
	cmdAdd.Dir = dir
	if out, err := cmdAdd.CombinedOutput(); err != nil {
		return fmt.Errorf("add: %s", string(out))
	}

	msg := fmt.Sprintf("Auto-save: %s (via dgGit)", filename)

	cmdCommit := exec.Command("git", "commit", "-m", msg)
	cmdCommit.Dir = dir
	if out, err := cmdCommit.CombinedOutput(); err != nil {
		return fmt.Errorf("commit: %s", string(out))
	}
	return nil
}

func askForFolder(startDir string) string {
	builder := dialog.Directory().Title("Be sure you have COPIED YOUR CODE TO THE CLIPBOARD FIRST and then select folder to save code into")

	if info, err := os.Stat(startDir); err == nil && info.IsDir() {
		builder = builder.SetStartDir(startDir)
	}

	dir, err := builder.Browse()
	if err != nil {
		return ""
	}
	return dir
}

func sanitizeFilename(name string) string {
	illegalChars := []string{"<", ">", ":", "\"", "/", "\\", "|", "?", "*"}
	for _, char := range illegalChars {
		name = strings.ReplaceAll(name, char, "_")
	}
	return name
}

func getExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// --- REGISTRY ---

func updateRegistry() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	if err := setMenuKey(`Software\Classes\Directory\Background\shell`, exePath, false); err != nil {
		return err
	}
	if err := setMenuKey(`Software\Classes\Directory\shell`, exePath, true); err != nil {
		return err
	}

	fileKeyPath := `Software\Classes\*\shell\` + AppName
	_ = registry.DeleteKey(registry.CURRENT_USER, fileKeyPath+`\command`)
	_ = registry.DeleteKey(registry.CURRENT_USER, fileKeyPath)
	return nil
}

func setMenuKey(basePath, exePath string, passArg bool) error {
	keyPath := basePath + `\` + AppName
	cmdPath := keyPath + `\command`
	k, _, err := registry.CreateKey(registry.CURRENT_USER, keyPath, registry.ALL_ACCESS)
	if err != nil {
		return err
	}
	defer k.Close()
	k.SetStringValue("", AppName)
	k.SetStringValue("Icon", "shell32.dll,259")
	ck, _, err := registry.CreateKey(registry.CURRENT_USER, cmdPath, registry.ALL_ACCESS)
	if err != nil {
		return err
	}
	defer ck.Close()
	cmdStr := fmt.Sprintf(`"%s"`, exePath)
	if passArg {
		cmdStr = fmt.Sprintf(`"%s" "%%1"`, exePath)
	}
	curr, _, _ := ck.GetStringValue("")
	if curr != cmdStr {
		ck.SetStringValue("", cmdStr)
	}
	return nil
}
