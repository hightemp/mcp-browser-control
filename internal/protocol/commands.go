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
