// The messages of the pak UI. Keep this file and the locale files in
// sync (checked by the tests).

package l10n

import "github.com/BrandonKowalski/gabagool/v2/pkg/gabagool/i18n"

// All UI messages, with their English text. Every ID must exist in each
// locales/active.<lang>.toml file (checked by the tests).

type msg = i18n.Message

// Footer buttons
var (
	BtnQuit       = &msg{ID: "button_quit", Other: "Quit"}
	BtnBack       = &msg{ID: "button_back", Other: "Back"}
	BtnSelect     = &msg{ID: "button_select", Other: "Select"}
	BtnCancel     = &msg{ID: "button_cancel", Other: "Cancel"}
	BtnConfirm    = &msg{ID: "button_confirm", Other: "Confirm"}
	BtnOK         = &msg{ID: "button_ok", Other: "OK"}
	BtnForget     = &msg{ID: "button_forget", Other: "Forget"}
	BtnSettings   = &msg{ID: "button_settings", Other: "Settings"}
	BtnHideQR     = &msg{ID: "button_hide_qr", Other: "Hide QR"}
	BtnShowQR     = &msg{ID: "button_show_qr", Other: "Show QR"}
	BtnDisconnect = &msg{ID: "button_disconnect", Other: "Disconnect"}
)

// Main screen
var (
	StateReady       = &msg{ID: "state_ready", Other: "Ready"}
	StateConnected   = &msg{ID: "state_connected", Other: "Connected"}
	StateTransfer    = &msg{ID: "state_transfer", Other: "Transfer in progress"}
	StateNoWiFi      = &msg{ID: "state_no_wifi", Other: "No Wi-Fi"}
	HintNoWiFi       = &msg{ID: "hint_no_wifi", Other: "The Wi-Fi is off: turn it on in the NextUI settings.\nReminisLink resumes as soon as it is back."}
	ScanHint         = &msg{ID: "scan_hint", Other: "Scan with the ReminisLink app"}
	QRHidden         = &msg{ID: "qr_hidden", Other: "QR code hidden: pairing is closed.\nPress A to show it."}
	BatteryLevel     = &msg{ID: "battery_level", Other: "{{.Percent}}%"}
	BatteryCharging  = &msg{ID: "battery_charging", Other: "{{.Percent}}% (charging)"}
	BatteryLow       = &msg{ID: "battery_low", Other: "Low: {{.Percent}}%"}
	BatteryLowBanner = &msg{ID: "battery_low_banner", Other: "Low battery: plug the console in"}
	QRUnavailable    = &msg{ID: "qr_unavailable", Other: "No QR code on this system: pair from the console."}
	HintPairMore     = &msg{ID: "hint_pair_more", Other: "To pair another device: Settings (Y) > Pair a New Device."}
	LabelQR          = &msg{ID: "label_qr", Other: "QR code"}
	PairingExpires   = &msg{ID: "pairing_expires", One: "Expires in {{.Count}} min", Other: "Expires in {{.Count}} min"}
	PairingBlocked   = &msg{ID: "pairing_blocked", One: "Too many failed attempts: pairing blocked for {{.Count}} min", Other: "Too many failed attempts: pairing blocked for {{.Count}} min"}
	LabelConsole     = &msg{ID: "label_console", Other: "Console"}
	ConsoleValue     = &msg{ID: "console_value", Other: "{{.Name}} · {{.IP}}"}
	LabelState       = &msg{ID: "label_state", Other: "State"}
	LabelConnected   = &msg{ID: "label_connected", Other: "Connected devices"}
	NoneConnected    = &msg{ID: "none_connected", Other: "None"}
	TitleTransfers   = &msg{ID: "title_transfers", Other: "Transfers"}
	ActUpload        = &msg{ID: "activity_upload", Other: "Receiving: {{.Name}}"}
	ActUploadFile    = &msg{ID: "activity_upload_file", Other: "Receiving a file"}
	ActPatch         = &msg{ID: "activity_patch", Other: "Patching: {{.Name}}"}
	ActPatchFile     = &msg{ID: "activity_patch_file", Other: "Applying a patch"}
	ActPak           = &msg{ID: "activity_pak", Other: "Installing: {{.Name}}"}
	ActPakFile       = &msg{ID: "activity_pak_file", Other: "Installing an app"}
	ActQueued        = &msg{ID: "activity_queued", Other: "{{.Text}} (waiting)"}
	ActProgress      = &msg{ID: "activity_progress", Other: "{{.Text}}  {{.Percent}}%"}
	ActBytes         = &msg{ID: "activity_bytes", Other: "{{.Text}}  {{.Size}}"}
	SizeMB           = &msg{ID: "size_mb", Other: "{{.Value}} MB"}
	DecimalSeparator = &msg{ID: "decimal_separator", Other: "."}
)

// Banner (SD card changes)
var (
	NoticeWithName          = &msg{ID: "notice_with_name", Other: "{{.Text}}: {{.Name}}"}
	NoticeROMAdded          = &msg{ID: "notice_rom_added", Other: "Game added"}
	NoticeSaveReplaced      = &msg{ID: "notice_save_replaced", Other: "Save replaced"}
	NoticeSaveRestored      = &msg{ID: "notice_save_restored", Other: "Save restored"}
	NoticeStateReplaced     = &msg{ID: "notice_state_replaced", Other: "State replaced"}
	NoticeStateRestored     = &msg{ID: "notice_state_restored", Other: "State restored"}
	NoticeStateDeleted      = &msg{ID: "notice_state_deleted", Other: "State moved to the trash"}
	NoticeGameDeleted       = &msg{ID: "notice_game_deleted", Other: "Game moved to the trash"}
	NoticeCheatsUpdated     = &msg{ID: "notice_cheats_updated", Other: "Cheats updated"}
	NoticeTrashEmptied      = &msg{ID: "notice_trash_emptied", Other: "Trash emptied"}
	NoticeTrashEntryDeleted = &msg{ID: "notice_trash_entry_deleted", Other: "Trash entry deleted"}
	NoticePatched           = &msg{ID: "notice_patched", Other: "Patch applied"}
	NoticePatchFailed       = &msg{ID: "notice_patch_failed", Other: "Patch failed"}
	NoticePaired            = &msg{ID: "notice_paired", Other: "Paired with {{.Name}}"}
	NoticeArtSet            = &msg{ID: "notice_art_set", Other: "Art updated"}
	NoticeArtDeleted        = &msg{ID: "notice_art_deleted", Other: "Art moved to the trash"}
	NoticePakInstalled      = &msg{ID: "notice_pak_installed", Other: "App installed"}
	NoticePakInstallFailed  = &msg{ID: "notice_pak_install_failed", Other: "App installation failed"}
	NoticePakDeleted        = &msg{ID: "notice_pak_deleted", Other: "App moved to the trash"}
)

// Other screens
var (
	MsgNoWiFi      = &msg{ID: "msg_no_wifi", Other: "No Wi-Fi.\nTurn on Wi-Fi in the NextUI settings."}
	MsgPreparing   = &msg{ID: "msg_preparing", Other: "Preparing…"}
	MsgClosing     = &msg{ID: "msg_closing", Other: "Closing…"}
	MsgStartError  = &msg{ID: "msg_start_error", Other: "ReminisLink could not start:\n{{.Error}}"}
	MsgConfirmQuit = &msg{ID: "msg_confirm_quit", Other: "A transfer is in progress. Quit anyway?"}
	LanguagePrompt = &msg{ID: "language_prompt", Other: "Choose the interface language."}
)

// Settings
var (
	TitleSettings       = &msg{ID: "title_settings", Other: "Settings"}
	ItemPairNew         = &msg{ID: "item_pair_new", Other: "Pair a New Device"}
	ItemName            = &msg{ID: "item_name", Other: "Console name: {{.Name}}"}
	ItemPaired          = &msg{ID: "item_paired", One: "Devices ({{.Count}})", Other: "Devices ({{.Count}})"}
	ItemForgetAll       = &msg{ID: "item_forget_all", Other: "Forget all devices"}
	ItemLanguage        = &msg{ID: "item_language", Other: "Language: {{.Name}}"}
	ItemAbout           = &msg{ID: "item_about", Other: "Version and log"}
	MsgAbout            = &msg{ID: "msg_about", Other: "ReminisLink {{.Version}}\n\nLog:\n{{.Path}}"}
	TitlePaired         = &msg{ID: "title_paired", Other: "Devices"}
	MsgNoPaired         = &msg{ID: "msg_no_paired", Other: "No paired device"}
	PairedItem          = &msg{ID: "paired_item", Other: "{{.Name}} (paired {{.Date}})"}
	DateLayout          = &msg{ID: "date_layout", Other: "2006-01-02"}
	MsgConfirmForget    = &msg{ID: "msg_confirm_forget", Other: "Forget {{.Name}}?\nThis device will have to pair again."}
	MsgConfirmForgetAll = &msg{ID: "msg_confirm_forget_all", Other: "Forget all paired devices?\nEach device will have to pair again."}
	MsgForgotAll        = &msg{ID: "msg_forgot_all", One: "{{.Count}} device forgotten.", Other: "{{.Count}} devices forgotten."}
	MsgNameEmpty        = &msg{ID: "msg_name_empty", Other: "The name cannot be empty."}
	MsgSaveError        = &msg{ID: "msg_save_error", Other: "Cannot save the settings:\n{{.Error}}"}
)

// Settings > Devices (M10, M13: platforms)
var (
	DeviceItem              = &msg{ID: "device_item", Other: "{{.Name}} · {{.Platform}} ({{.State}})"}
	DeviceIdleItem          = &msg{ID: "device_idle_item", Other: "{{.Name}} · {{.Platform}} (paired {{.Date}})"}
	PlatformApple           = &msg{ID: "platform_apple", Other: "Apple"}
	PlatformAndroid         = &msg{ID: "platform_android", Other: "Android"}
	PlatformWeb             = &msg{ID: "platform_web", Other: "Web"}
	PlatformOther           = &msg{ID: "platform_other", Other: "Other"}
	DeviceStateConnected    = &msg{ID: "device_state_connected", Other: "connected"}
	DeviceStateDisconnected = &msg{ID: "device_state_disconnected", Other: "disconnected"}
	DeviceStateSeparator    = &msg{ID: "device_state_separator", Other: ", "}
	MsgDisconnected         = &msg{ID: "msg_disconnected", Other: "{{.Name}} is disconnected.\nIt can reconnect from the app."}
	MsgDeviceError          = &msg{ID: "msg_device_error", Other: "Cannot change this device:\n{{.Error}}"}
)

// All lists every message, for the tests.
var All = []*msg{
	BtnQuit, BtnBack, BtnSelect, BtnCancel, BtnConfirm, BtnOK, BtnForget, BtnSettings, BtnHideQR,
	BtnShowQR, StateReady, StateConnected, StateTransfer, StateNoWiFi, HintNoWiFi, ScanHint, QRHidden,
	LabelQR, PairingExpires, PairingBlocked, LabelConsole, ConsoleValue, LabelState, LabelConnected,
	NoneConnected, TitleTransfers, ActUpload, ActUploadFile, ActPatch,
	ActPatchFile, ActQueued, ActProgress, ActBytes, SizeMB, DecimalSeparator, NoticeWithName,
	NoticeROMAdded, NoticeSaveReplaced, NoticeSaveRestored, NoticeStateReplaced, NoticeStateRestored,
	NoticeStateDeleted, NoticeGameDeleted, NoticeCheatsUpdated, NoticeTrashEmptied,
	NoticeTrashEntryDeleted, NoticePatched, NoticePatchFailed,
	NoticePaired, MsgNoWiFi, MsgPreparing, MsgClosing, MsgStartError, MsgConfirmQuit, LanguagePrompt,
	TitleSettings, ItemName, ItemPaired, ItemForgetAll, ItemLanguage, ItemAbout, MsgAbout,
	TitlePaired, MsgNoPaired, PairedItem, DateLayout, MsgConfirmForget, MsgConfirmForgetAll,
	MsgForgotAll, MsgNameEmpty, MsgSaveError, BatteryLevel, BatteryCharging, BatteryLow, BatteryLowBanner, QRUnavailable, HintPairMore, NoticeArtSet, NoticeArtDeleted, ItemPairNew,
	BtnDisconnect, DeviceItem, DeviceIdleItem, PlatformApple, PlatformAndroid, PlatformWeb, PlatformOther,
	DeviceStateConnected, DeviceStateDisconnected, DeviceStateSeparator, MsgDisconnected, MsgDeviceError,
	ActPak, ActPakFile, NoticePakInstalled, NoticePakInstallFailed, NoticePakDeleted,
}
