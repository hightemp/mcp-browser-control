package protocol

const (
	// CommandBrowserPing checks extension responsiveness.
	CommandBrowserPing = "browser.ping"

	// CommandWindowsList returns browser windows.
	CommandWindowsList = "windows.list"

	// CommandWindowsGet returns one browser window.
	CommandWindowsGet = "windows.get"

	// CommandWindowsCreate creates a browser window.
	CommandWindowsCreate = "windows.create"

	// CommandWindowsUpdate changes window bounds or state.
	CommandWindowsUpdate = "windows.update"

	// CommandWindowsFocus focuses a browser window.
	CommandWindowsFocus = "windows.focus"

	// CommandWindowsClose closes a browser window.
	CommandWindowsClose = "windows.close"

	// CommandTabsList returns tabs in the selected browser.
	CommandTabsList = "tabs.list"

	// CommandTabsGet returns one browser tab.
	CommandTabsGet = "tabs.get"

	// CommandTabsCreate creates a browser tab.
	CommandTabsCreate = "tabs.create"

	// CommandTabsActivate activates a browser tab.
	CommandTabsActivate = "tabs.activate"

	// CommandTabsNavigate navigates a browser tab.
	CommandTabsNavigate = "tabs.navigate"

	// CommandTabsReload reloads a browser tab.
	CommandTabsReload = "tabs.reload"

	// CommandTabsStop stops loading a browser tab.
	CommandTabsStop = "tabs.stop"

	// CommandTabsBack navigates a browser tab backward.
	CommandTabsBack = "tabs.back"

	// CommandTabsForward navigates a browser tab forward.
	CommandTabsForward = "tabs.forward"

	// CommandTabsMove moves a browser tab.
	CommandTabsMove = "tabs.move"

	// CommandTabsDuplicate duplicates a browser tab.
	CommandTabsDuplicate = "tabs.duplicate"

	// CommandTabsClose closes a browser tab.
	CommandTabsClose = "tabs.close"

	// CommandTabsPin changes a browser tab's pinned state.
	CommandTabsPin = "tabs.pin"

	// CommandTabsMute changes a browser tab's muted state.
	CommandTabsMute = "tabs.mute"

	// CommandTabsGetZoom returns a browser tab's zoom factor.
	CommandTabsGetZoom = "tabs.getZoom"

	// CommandTabsSetZoom changes a browser tab's zoom factor.
	CommandTabsSetZoom = "tabs.setZoom"

	// CommandPageGetHTML returns page or element HTML.
	CommandPageGetHTML = "page.getHTML"

	// CommandPageGetHTMLBySelector returns HTML for matching elements.
	CommandPageGetHTMLBySelector = "page.getHTMLBySelector"

	// CommandPageClick clicks a page element.
	CommandPageClick = "page.click"

	// CommandPageFill fills a page input.
	CommandPageFill = "page.fill"

	// CommandConsoleRead reads captured console entries.
	CommandConsoleRead = "console.read"

	// CommandNetworkRead reads captured network entries.
	CommandNetworkRead = "network.read"
)
