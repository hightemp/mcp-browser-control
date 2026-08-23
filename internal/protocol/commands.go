package protocol

const (
	// CommandBrowserPing checks extension responsiveness.
	CommandBrowserPing = "browser.ping"

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
