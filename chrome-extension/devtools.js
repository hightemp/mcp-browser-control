// DevTools script для создания панели расширения

// Создаем панель в DevTools
chrome.devtools.panels.create(
  'Browser Tool', // Название панели
  'icons/icon16.png', // Иконка панели
  'panel.html', // HTML файл панели
  function(panel) {
    console.log('DevTools панель создана');
    
    // Обработчик показа панели
    panel.onShown.addListener(function(panelWindow) {
      console.log('DevTools панель показана');
      
      // Передаем информацию о текущей вкладке в панель
      panelWindow.currentTabId = chrome.devtools.inspectedWindow.tabId;
      
      // Инициализируем панель
      if (panelWindow.initializePanel) {
        panelWindow.initializePanel();
      }
    });
    
    // Обработчик скрытия панели
    panel.onHidden.addListener(function() {
      console.log('DevTools панель скрыта');
    });
  }
);

// Слушаем изменения в инспектируемом окне
chrome.devtools.network.onNavigated.addListener(function(url) {
  console.log('Навигация на новую страницу:', url);
});