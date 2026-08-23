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

	// CommandTabsGroup adds tabs to a new or existing group.
	CommandTabsGroup = "tabs.group"

	// CommandTabsUngroup removes tabs from their groups.
	CommandTabsUngroup = "tabs.ungroup"

	// CommandTabGroupsUpdate changes tab group presentation.
	CommandTabGroupsUpdate = "tabGroups.update"

	// CommandSessionsRecentlyClosed lists recently closed tabs and windows.
	CommandSessionsRecentlyClosed = "sessions.recentlyClosed"

	// CommandSessionsRestore reopens a recently closed tab or window.
	CommandSessionsRestore = "sessions.restore"

	// CommandPageGetHTML returns page or element HTML.
	CommandPageGetHTML = "page.getHTML"

	// CommandPageInfo returns bounded page and frame metadata.
	CommandPageInfo = "page.info"

	// CommandPageGetText returns normalized visible page text.
	CommandPageGetText = "page.getText"

	// CommandPageQuery returns paginated locator matches.
	CommandPageQuery = "page.query"

	// CommandPageGetElement returns normalized details for one element.
	CommandPageGetElement = "page.getElement"

	// CommandPageSnapshot returns a compact semantic page tree.
	CommandPageSnapshot = "page.snapshot"

	// CommandPageGetHTMLBySelector returns HTML for matching elements.
	CommandPageGetHTMLBySelector = "page.getHTMLBySelector"

	// CommandPageClick clicks a page element.
	CommandPageClick = "page.click"

	// CommandPageFill fills a page input.
	CommandPageFill = "page.fill"

	// CommandPageHover moves the synthetic pointer over an element.
	CommandPageHover = "page.hover"
	// CommandPageFocus focuses an element.
	CommandPageFocus = "page.focus"
	// CommandPageBlur removes focus from an element.
	CommandPageBlur = "page.blur"
	// CommandPageType appends text through input events.
	CommandPageType = "page.type"
	// CommandPageClear clears an editable element.
	CommandPageClear = "page.clear"
	// CommandPagePress dispatches a keyboard chord.
	CommandPagePress = "page.press"
	// CommandPageSelect selects option values.
	CommandPageSelect = "page.select"
	// CommandPageSetChecked sets or toggles a checkable element.
	CommandPageSetChecked = "page.setChecked"
	// CommandPageScroll scrolls the page or an element.
	CommandPageScroll = "page.scroll"
	// CommandPageDrag drags one element to another target.
	CommandPageDrag = "page.drag"
	// CommandPageDispatch dispatches a validated DOM event shape.
	CommandPageDispatch = "page.dispatch"
	// CommandPageSubmit submits a form or an element's owning form.
	CommandPageSubmit = "page.submit"

	// CommandPageWait waits for a bounded page or browser condition.
	CommandPageWait = "page.wait"

	// CommandConsoleRead reads captured console entries.
	CommandConsoleRead = "console.read"

	// CommandNetworkRead reads captured network entries.
	CommandNetworkRead = "network.read"
)
