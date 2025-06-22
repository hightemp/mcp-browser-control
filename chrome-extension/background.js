// Background script для расширения Chrome
class WebSocketManager {
  constructor() {
    this.ws = null;
    this.isConnected = false;
    this.reconnectAttempts = 0;
    this.maxReconnectAttempts = 10;
    this.reconnectDelay = 1000;
    this.serverUrl = 'ws://localhost:8090/ws';
    this.messageQueue = [];
    this.pingInterval = null;
    this.pingIntervalTime = 30000; // 30 секунд
    this.lastPongTime = Date.now();
    this.connectionTimeout = 60000; // 60 секунд тайм-аут
  }

  connect() {
    try {
      this.ws = new WebSocket(this.serverUrl);
      
      this.ws.onopen = () => {
        console.log('WebSocket соединение установлено');
        this.isConnected = true;
        this.reconnectAttempts = 0;
        this.lastPongTime = Date.now();
        this.startPingInterval();
        this.processMessageQueue();
      };

      this.ws.onmessage = (event) => {
        try {
          // Обработка pong сообщений
          if (event.data === 'pong') {
            this.lastPongTime = Date.now();
            console.log('Получен pong от сервера');
            return;
          }
          
          const message = JSON.parse(event.data);
          this.handleMessage(message);
        } catch (error) {
          console.error('Ошибка парсинга сообщения:', error);
        }
      };

      this.ws.onclose = (event) => {
        console.log('WebSocket соединение закрыто', event.code, event.reason);
        this.isConnected = false;
        this.stopPingInterval();
        this.attemptReconnect();
      };

      this.ws.onerror = (error) => {
        console.error('Ошибка WebSocket:', error);
        this.isConnected = false;
      };
    } catch (error) {
      console.error('Ошибка создания WebSocket соединения:', error);
      this.attemptReconnect();
    }
  }

  attemptReconnect() {
    if (this.reconnectAttempts < this.maxReconnectAttempts) {
      this.reconnectAttempts++;
      console.log(`Попытка переподключения ${this.reconnectAttempts}/${this.maxReconnectAttempts}`);
      setTimeout(() => {
        this.connect();
      }, this.reconnectDelay * this.reconnectAttempts);
    } else {
      console.error('Превышено максимальное количество попыток переподключения');
    }
  }

  sendMessage(message) {
    if (this.isConnected && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(message));
    } else {
      console.log('WebSocket не подключен, добавляем сообщение в очередь');
      this.messageQueue.push(message);
      if (!this.isConnected) {
        this.connect();
      }
    }
  }

  processMessageQueue() {
    while (this.messageQueue.length > 0) {
      const message = this.messageQueue.shift();
      this.sendMessage(message);
    }
  }

  handleMessage(message) {
    console.log('Получено сообщение от сервера:', message);
    
    // Обрабатываем команды от сервера
    if (message.command) {
      this.handleServerCommand(message);
    }
    
    // Отправляем сообщение в DevTools панель
    chrome.runtime.sendMessage({
      type: 'SERVER_RESPONSE',
      data: message
    }).catch(error => {
      console.log('DevTools панель не активна:', error);
    });
  }

  async handleServerCommand(message) {
    console.log('Обработка команды от сервера:', message);
    
    try {
      let response = null;
      
      // Получаем активную вкладку
      const tabs = await chrome.tabs.query({ active: true, currentWindow: true });
      const activeTab = tabs[0];
      
      if (!activeTab) {
        throw new Error('Нет активной вкладки');
      }
      
      switch (message.command) {
        case 'GET_HTML':
          response = await chrome.tabs.sendMessage(activeTab.id, {
            type: 'GET_HTML',
            params: message.params || {}
          });
          break;
          
        case 'GET_HTML_BY_SELECTOR':
          response = await chrome.tabs.sendMessage(activeTab.id, {
            type: 'GET_HTML_BY_SELECTOR',
            params: message.params || {}
          });
          break;
          
        case 'CLICK_ELEMENT':
          response = await chrome.tabs.sendMessage(activeTab.id, {
            type: 'CLICK_ELEMENT',
            params: message.params || {}
          });
          break;
          
        case 'INPUT_DATA':
          response = await chrome.tabs.sendMessage(activeTab.id, {
            type: 'INPUT_DATA',
            params: message.params || {}
          });
          break;
          
        case 'GET_CONSOLE_LOG':
          response = await this.getConsoleLogs(activeTab.id);
          break;
          
        case 'GET_NETWORK_LOG':
          response = await this.getNetworkLogs(activeTab.id);
          break;
          
        case 'GET_TABS':
          response = await this.getAllTabs();
          break;
          
        default:
          throw new Error(`Неизвестная команда: ${message.command}`);
      }
      
      // Отправляем ответ обратно на сервер
      this.sendMessage({
        id: message.id,
        command: message.command,
        data: response,
        success: true,
        tabId: activeTab.id
      });
      
    } catch (error) {
      console.error('Ошибка выполнения команды:', error);
      
      // Отправляем ошибку обратно на сервер
      this.sendMessage({
        id: message.id,
        command: message.command,
        data: null,
        success: false,
        error: error.message
      });
    }
  }

  async getConsoleLogs(tabId) {
    return new Promise((resolve, reject) => {
      chrome.debugger.attach({ tabId }, "1.0", () => {
        if (chrome.runtime.lastError) {
          reject(new Error(chrome.runtime.lastError.message));
          return;
        }
        
        chrome.debugger.sendCommand({ tabId }, "Runtime.enable", {}, () => {
          chrome.debugger.sendCommand({ tabId }, "Log.enable", {}, () => {
            chrome.debugger.sendCommand({ tabId }, "Runtime.getConsoleAPICalls", {}, (result) => {
              chrome.debugger.detach({ tabId });
              resolve(result);
            });
          });
        });
      });
    });
  }

  async getAllTabs() {
    try {
      // Получаем все вкладки
      const allTabs = await chrome.tabs.query({});
      
      // Получаем активную вкладку в текущем окне
      const activeTabs = await chrome.tabs.query({ active: true, currentWindow: true });
      const activeTabId = activeTabs.length > 0 ? activeTabs[0].id : null;
      
      // Формируем список вкладок с информацией
      const tabsInfo = allTabs.map(tab => ({
        id: tab.id,
        title: tab.title,
        url: tab.url,
        active: tab.id === activeTabId,
        windowId: tab.windowId,
        index: tab.index,
        pinned: tab.pinned,
        status: tab.status, // loading, complete
        favIconUrl: tab.favIconUrl,
        incognito: tab.incognito
      }));
      
      return {
        success: true,
        tabs: tabsInfo,
        totalCount: tabsInfo.length,
        activeTabId: activeTabId,
        timestamp: new Date().toISOString()
      };
      
    } catch (error) {
      throw new Error(`Ошибка получения списка вкладок: ${error.message}`);
    }
  }

  async getNetworkLogs(tabId) {
    return new Promise((resolve, reject) => {
      chrome.debugger.attach({ tabId }, "1.0", () => {
        if (chrome.runtime.lastError) {
          reject(new Error(chrome.runtime.lastError.message));
          return;
        }
        
        chrome.debugger.sendCommand({ tabId }, "Network.enable", {}, () => {
          // Для network логов нужна более сложная логика
          // Пока возвращаем заглушку
          chrome.debugger.detach({ tabId });
          resolve({ message: "Network logs feature in development" });
        });
      });
    });
  }

  disconnect() {
    if (this.ws) {
      this.stopPingInterval();
      this.ws.close();
      this.ws = null;
      this.isConnected = false;
    }
  }

  startPingInterval() {
    this.stopPingInterval();
    this.pingInterval = setInterval(() => {
      if (this.isConnected && this.ws && this.ws.readyState === WebSocket.OPEN) {
        // Проверяем, получали ли мы pong в течение тайм-аута
        const timeSinceLastPong = Date.now() - this.lastPongTime;
        if (timeSinceLastPong > this.connectionTimeout) {
          console.warn('Не получен pong в течение', this.connectionTimeout, 'мс. Переподключение...');
          this.ws.close();
          return;
        }
        
        // Отправляем ping
        try {
          this.ws.send('ping');
          console.log('Отправлен ping серверу');
        } catch (error) {
          console.error('Ошибка отправки ping:', error);
          this.ws.close();
        }
      }
    }, this.pingIntervalTime);
  }

  stopPingInterval() {
    if (this.pingInterval) {
      clearInterval(this.pingInterval);
      this.pingInterval = null;
    }
  }
}

// Создаем экземпляр WebSocket менеджера
const wsManager = new WebSocketManager();

// Обработчик сообщений от content script и DevTools панели
chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  console.log('Получено сообщение:', message);

  switch (message.type) {
    case 'CONNECT_WEBSOCKET':
      wsManager.connect();
      sendResponse({ success: true });
      break;

    case 'DISCONNECT_WEBSOCKET':
      wsManager.disconnect();
      sendResponse({ success: true });
      break;

    case 'SEND_COMMAND':
      wsManager.sendMessage({
        command: message.command,
        params: message.params,
        tabId: sender.tab?.id
      });
      sendResponse({ success: true });
      break;

    case 'GET_HTML':
      // Отправляем команду в content script
      if (sender.tab) {
        chrome.tabs.sendMessage(sender.tab.id, {
          type: 'GET_HTML',
          params: message.params
        }).then(response => {
          wsManager.sendMessage({
            command: 'get_html',
            params: message.params,
            data: response,
            tabId: sender.tab.id
          });
        });
      }
      sendResponse({ success: true });
      break;

    case 'GET_HTML_BY_SELECTOR':
      if (sender.tab) {
        chrome.tabs.sendMessage(sender.tab.id, {
          type: 'GET_HTML_BY_SELECTOR',
          params: message.params
        }).then(response => {
          wsManager.sendMessage({
            command: 'get_html_by_selector',
            params: message.params,
            data: response,
            tabId: sender.tab.id
          });
        });
      }
      sendResponse({ success: true });
      break;

    case 'CLICK_ELEMENT':
      if (sender.tab) {
        chrome.tabs.sendMessage(sender.tab.id, {
          type: 'CLICK_ELEMENT',
          params: message.params
        }).then(response => {
          wsManager.sendMessage({
            command: 'click_element',
            params: message.params,
            data: response,
            tabId: sender.tab.id
          });
        });
      }
      sendResponse({ success: true });
      break;

    case 'INPUT_DATA':
      if (sender.tab) {
        chrome.tabs.sendMessage(sender.tab.id, {
          type: 'INPUT_DATA',
          params: message.params
        }).then(response => {
          wsManager.sendMessage({
            command: 'input_data',
            params: message.params,
            data: response,
            tabId: sender.tab.id
          });
        });
      }
      sendResponse({ success: true });
      break;

    case 'GET_CONSOLE_LOG':
      // Для получения логов консоли используем debugger API
      if (sender.tab) {
        chrome.debugger.attach({ tabId: sender.tab.id }, "1.0", () => {
          chrome.debugger.sendCommand({ tabId: sender.tab.id }, "Runtime.enable", {}, () => {
            chrome.debugger.sendCommand({ tabId: sender.tab.id }, "Log.enable", {}, () => {
              // Получаем логи консоли
              chrome.debugger.sendCommand({ tabId: sender.tab.id }, "Runtime.getConsoleAPICalls", {}, (result) => {
                wsManager.sendMessage({
                  command: 'get_console_log',
                  params: message.params,
                  data: result,
                  tabId: sender.tab.id
                });
                chrome.debugger.detach({ tabId: sender.tab.id });
              });
            });
          });
        });
      }
      sendResponse({ success: true });
      break;

    case 'GET_NETWORK_LOG':
      // Для получения network логов используем debugger API
      if (sender.tab) {
        chrome.debugger.attach({ tabId: sender.tab.id }, "1.0", () => {
          chrome.debugger.sendCommand({ tabId: sender.tab.id }, "Network.enable", {}, () => {
            // Получаем network логи
            chrome.debugger.sendCommand({ tabId: sender.tab.id }, "Network.getResponseBody", {}, (result) => {
              wsManager.sendMessage({
                command: 'get_network_log',
                params: message.params,
                data: result,
                tabId: sender.tab.id
              });
              chrome.debugger.detach({ tabId: sender.tab.id });
            });
          });
        });
      }
      sendResponse({ success: true });
      break;

    default:
      console.log('Неизвестный тип сообщения:', message.type);
      sendResponse({ success: false, error: 'Unknown message type' });
  }

  return true; // Указывает, что ответ будет отправлен асинхронно
});

// Автоматическое подключение при запуске расширения
chrome.runtime.onStartup.addListener(() => {
  wsManager.connect();
});

chrome.runtime.onInstalled.addListener(() => {
  wsManager.connect();
});