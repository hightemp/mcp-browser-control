// JavaScript для панели DevTools

class DevToolsPanel {
    constructor() {
        this.currentTabId = null;
        this.isConnected = false;
        this.results = [];
        this.resultCounter = 0;
        
        this.initializeElements();
        this.setupEventListeners();
        this.setupMessageListener();
    }

    initializeElements() {
        // Status elements
        this.connectionStatus = document.getElementById('connectionStatus');
        this.connectBtn = document.getElementById('connectBtn');
        
        // Command buttons
        this.getHtmlBtn = document.getElementById('getHtmlBtn');
        this.getHtmlBySelectorBtn = document.getElementById('getHtmlBySelectorBtn');
        this.getConsoleLogBtn = document.getElementById('getConsoleLogBtn');
        this.getNetworkLogBtn = document.getElementById('getNetworkLogBtn');
        this.clickElementBtn = document.getElementById('clickElementBtn');
        this.inputDataBtn = document.getElementById('inputDataBtn');
        
        // Input elements
        this.selectorInput = document.getElementById('selectorInput');
        this.clickSelectorInput = document.getElementById('clickSelectorInput');
        this.inputSelectorInput = document.getElementById('inputSelectorInput');
        this.inputValueInput = document.getElementById('inputValueInput');
        
        // Results elements
        this.resultsCount = document.getElementById('resultsCount');
        this.clearResultsBtn = document.getElementById('clearResultsBtn');
        this.resultsContent = document.getElementById('resultsContent');
        
        // Modal elements
        this.modal = document.getElementById('modal');
        this.modalTitle = document.getElementById('modalTitle');
        this.modalContent = document.getElementById('modalContent');
        this.modalCloseBtn = document.getElementById('modalCloseBtn');
        this.copyModalContentBtn = document.getElementById('copyModalContentBtn');
    }

    setupEventListeners() {
        // Connection
        this.connectBtn.addEventListener('click', () => this.toggleConnection());
        
        // Commands
        this.getHtmlBtn.addEventListener('click', () => this.executeCommand('GET_HTML'));
        this.getHtmlBySelectorBtn.addEventListener('click', () => this.executeCommand('GET_HTML_BY_SELECTOR'));
        this.getConsoleLogBtn.addEventListener('click', () => this.executeCommand('GET_CONSOLE_LOG'));
        this.getNetworkLogBtn.addEventListener('click', () => this.executeCommand('GET_NETWORK_LOG'));
        this.clickElementBtn.addEventListener('click', () => this.executeCommand('CLICK_ELEMENT'));
        this.inputDataBtn.addEventListener('click', () => this.executeCommand('INPUT_DATA'));
        
        // Results
        this.clearResultsBtn.addEventListener('click', () => this.clearResults());
        
        // Modal
        this.modalCloseBtn.addEventListener('click', () => this.closeModal());
        this.copyModalContentBtn.addEventListener('click', () => this.copyModalContent());
        
        // Close modal on background click
        this.modal.addEventListener('click', (e) => {
            if (e.target === this.modal) {
                this.closeModal();
            }
        });
        
        // Keyboard shortcuts
        document.addEventListener('keydown', (e) => {
            if (e.key === 'Escape') {
                this.closeModal();
            }
        });
    }

    setupMessageListener() {
        // Слушаем сообщения от background script
        chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
            if (message.type === 'SERVER_RESPONSE') {
                this.handleServerResponse(message.data);
            }
        });
    }

    toggleConnection() {
        if (this.isConnected) {
            this.disconnect();
        } else {
            this.connect();
        }
    }

    connect() {
        this.updateConnectionStatus('connecting');
        
        chrome.runtime.sendMessage({
            type: 'CONNECT_WEBSOCKET'
        }, (response) => {
            if (response && response.success) {
                this.isConnected = true;
                this.updateConnectionStatus('connected');
            } else {
                this.updateConnectionStatus('disconnected');
                this.addResult('Ошибка подключения', { error: 'Не удалось подключиться к серверу' }, 'error');
            }
        });
    }

    disconnect() {
        chrome.runtime.sendMessage({
            type: 'DISCONNECT_WEBSOCKET'
        }, (response) => {
            this.isConnected = false;
            this.updateConnectionStatus('disconnected');
        });
    }

    updateConnectionStatus(status) {
        this.connectionStatus.className = `status-${status}`;
        
        switch (status) {
            case 'connected':
                this.connectionStatus.textContent = 'Подключено';
                this.connectBtn.textContent = 'Отключиться';
                this.connectBtn.className = 'btn btn-secondary';
                this.enableCommands(true);
                break;
            case 'connecting':
                this.connectionStatus.textContent = 'Подключение...';
                this.connectBtn.textContent = 'Подключение...';
                this.connectBtn.disabled = true;
                this.enableCommands(false);
                break;
            case 'disconnected':
                this.connectionStatus.textContent = 'Отключено';
                this.connectBtn.textContent = 'Подключиться';
                this.connectBtn.className = 'btn btn-primary';
                this.connectBtn.disabled = false;
                this.enableCommands(false);
                break;
        }
    }

    enableCommands(enabled) {
        const buttons = [
            this.getHtmlBtn,
            this.getHtmlBySelectorBtn,
            this.getConsoleLogBtn,
            this.getNetworkLogBtn,
            this.clickElementBtn,
            this.inputDataBtn
        ];
        
        buttons.forEach(btn => {
            btn.disabled = !enabled;
        });
    }

    executeCommand(commandType) {
        if (!this.isConnected) {
            this.addResult('Ошибка', { error: 'Нет подключения к серверу' }, 'error');
            return;
        }

        let params = {};
        let commandName = commandType;

        switch (commandType) {
            case 'GET_HTML_BY_SELECTOR':
                const selector = this.selectorInput.value.trim();
                if (!selector) {
                    this.addResult('Ошибка', { error: 'Укажите CSS селектор' }, 'error');
                    return;
                }
                params.selector = selector;
                break;
                
            case 'CLICK_ELEMENT':
                const clickSelector = this.clickSelectorInput.value.trim();
                if (!clickSelector) {
                    this.addResult('Ошибка', { error: 'Укажите CSS селектор для клика' }, 'error');
                    return;
                }
                params.selector = clickSelector;
                break;
                
            case 'INPUT_DATA':
                const inputSelector = this.inputSelectorInput.value.trim();
                const inputValue = this.inputValueInput.value;
                if (!inputSelector) {
                    this.addResult('Ошибка', { error: 'Укажите CSS селектор для ввода' }, 'error');
                    return;
                }
                if (!inputValue) {
                    this.addResult('Ошибка', { error: 'Укажите значение для ввода' }, 'error');
                    return;
                }
                params.selector = inputSelector;
                params.value = inputValue;
                break;
        }

        // Отправляем команду через background script
        chrome.runtime.sendMessage({
            type: commandType,
            params: params
        }, (response) => {
            if (response && response.success) {
                this.addResult(`Команда отправлена: ${commandName}`, params, 'success');
            } else {
                this.addResult('Ошибка отправки команды', response, 'error');
            }
        });
    }

    handleServerResponse(data) {
        this.addResult('Ответ сервера', data, data.success ? 'success' : 'error');
    }

    addResult(title, data, type = 'info') {
        this.resultCounter++;
        const timestamp = new Date().toLocaleTimeString();
        
        const result = {
            id: this.resultCounter,
            title: title,
            data: data,
            type: type,
            timestamp: timestamp
        };
        
        this.results.unshift(result);
        this.updateResultsDisplay();
    }

    updateResultsDisplay() {
        this.resultsCount.textContent = `${this.results.length} результатов`;
        
        if (this.results.length === 0) {
            this.resultsContent.innerHTML = '<p class="no-results">Результаты команд будут отображаться здесь</p>';
            return;
        }
        
        const resultsHtml = this.results.map(result => this.createResultHtml(result)).join('');
        this.resultsContent.innerHTML = resultsHtml;
        
        // Добавляем обработчики событий для новых элементов
        this.setupResultEventListeners();
    }

    createResultHtml(result) {
        const statusClass = `status-${result.type}`;
        const dataStr = typeof result.data === 'object' ? JSON.stringify(result.data, null, 2) : result.data;
        const truncatedData = dataStr.length > 200 ? dataStr.substring(0, 200) + '...' : dataStr;
        
        return `
            <div class="result-item" data-result-id="${result.id}">
                <div class="result-header">
                    <span class="result-title ${statusClass}">${result.title}</span>
                    <span class="result-timestamp">${result.timestamp}</span>
                </div>
                <div class="result-content">
                    <pre>${truncatedData}</pre>
                </div>
                <div class="result-actions">
                    <button class="btn btn-small view-full-btn" data-result-id="${result.id}">Показать полностью</button>
                    <button class="btn btn-small copy-btn" data-result-id="${result.id}">Копировать</button>
                </div>
            </div>
        `;
    }

    setupResultEventListeners() {
        // View full buttons
        document.querySelectorAll('.view-full-btn').forEach(btn => {
            btn.addEventListener('click', (e) => {
                const resultId = parseInt(e.target.dataset.resultId);
                this.showFullResult(resultId);
            });
        });
        
        // Copy buttons
        document.querySelectorAll('.copy-btn').forEach(btn => {
            btn.addEventListener('click', (e) => {
                const resultId = parseInt(e.target.dataset.resultId);
                this.copyResult(resultId);
            });
        });
    }

    showFullResult(resultId) {
        const result = this.results.find(r => r.id === resultId);
        if (!result) return;
        
        this.modalTitle.textContent = result.title;
        const dataStr = typeof result.data === 'object' ? JSON.stringify(result.data, null, 2) : result.data;
        this.modalContent.textContent = dataStr;
        this.modal.style.display = 'block';
    }

    copyResult(resultId) {
        const result = this.results.find(r => r.id === resultId);
        if (!result) return;
        
        const dataStr = typeof result.data === 'object' ? JSON.stringify(result.data, null, 2) : result.data;
        navigator.clipboard.writeText(dataStr).then(() => {
            // Показываем уведомление о копировании
            const btn = document.querySelector(`[data-result-id="${resultId}"].copy-btn`);
            const originalText = btn.textContent;
            btn.textContent = 'Скопировано!';
            setTimeout(() => {
                btn.textContent = originalText;
            }, 1000);
        });
    }

    copyModalContent() {
        navigator.clipboard.writeText(this.modalContent.textContent).then(() => {
            const originalText = this.copyModalContentBtn.textContent;
            this.copyModalContentBtn.textContent = 'Скопировано!';
            setTimeout(() => {
                this.copyModalContentBtn.textContent = originalText;
            }, 1000);
        });
    }

    closeModal() {
        this.modal.style.display = 'none';
    }

    clearResults() {
        this.results = [];
        this.resultCounter = 0;
        this.updateResultsDisplay();
    }
}

// Функция инициализации панели (вызывается из devtools.js)
function initializePanel() {
    console.log('Инициализация DevTools панели');
    window.devToolsPanel = new DevToolsPanel();
    
    // Устанавливаем текущий tab ID
    if (window.currentTabId) {
        window.devToolsPanel.currentTabId = window.currentTabId;
    }
}

// Инициализируем панель при загрузке
document.addEventListener('DOMContentLoaded', () => {
    initializePanel();
});

// Экспортируем для использования в devtools.js
window.initializePanel = initializePanel;