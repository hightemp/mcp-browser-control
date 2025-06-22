// Content script для взаимодействия с DOM страницы

class DOMInteractor {
  constructor() {
    this.setupMessageListener();
  }

  setupMessageListener() {
    chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
      console.log('Content script получил сообщение:', message);

      switch (message.type) {
        case 'GET_HTML':
          this.getHTML(message.params)
            .then(result => sendResponse(result))
            .catch(error => sendResponse({ error: error.message }));
          break;

        case 'GET_HTML_BY_SELECTOR':
          this.getHTMLBySelector(message.params)
            .then(result => sendResponse(result))
            .catch(error => sendResponse({ error: error.message }));
          break;

        case 'CLICK_ELEMENT':
          this.clickElement(message.params)
            .then(result => sendResponse(result))
            .catch(error => sendResponse({ error: error.message }));
          break;

        case 'INPUT_DATA':
          this.inputData(message.params)
            .then(result => sendResponse(result))
            .catch(error => sendResponse({ error: error.message }));
          break;

        default:
          sendResponse({ error: 'Unknown message type' });
      }

      return true; // Указывает, что ответ будет отправлен асинхронно
    });
  }

  async getHTML(params = {}) {
    try {
      const html = document.documentElement.outerHTML;
      return {
        success: true,
        html: html,
        url: window.location.href,
        title: document.title,
        timestamp: new Date().toISOString()
      };
    } catch (error) {
      throw new Error(`Ошибка получения HTML: ${error.message}`);
    }
  }

  async getHTMLBySelector(params) {
    try {
      const { selector } = params;
      if (!selector) {
        throw new Error('Селектор не указан');
      }

      const elements = document.querySelectorAll(selector);
      if (elements.length === 0) {
        return {
          success: true,
          html: '',
          count: 0,
          selector: selector,
          message: 'Элементы не найдены'
        };
      }

      let html = '';
      const elementsArray = [];

      elements.forEach((element, index) => {
        const elementHTML = element.outerHTML;
        html += elementHTML + '\n';
        elementsArray.push({
          index: index,
          tagName: element.tagName,
          className: element.className,
          id: element.id,
          html: elementHTML
        });
      });

      return {
        success: true,
        html: html.trim(),
        elements: elementsArray,
        count: elements.length,
        selector: selector,
        timestamp: new Date().toISOString()
      };
    } catch (error) {
      throw new Error(`Ошибка получения HTML по селектору: ${error.message}`);
    }
  }

  async clickElement(params) {
    try {
      const { selector, index = 0, coordinates } = params;

      let element;

      if (coordinates) {
        // Клик по координатам
        const { x, y } = coordinates;
        element = document.elementFromPoint(x, y);
        if (!element) {
          throw new Error(`Элемент не найден по координатам (${x}, ${y})`);
        }
      } else if (selector) {
        // Клик по селектору
        const elements = document.querySelectorAll(selector);
        if (elements.length === 0) {
          throw new Error(`Элемент не найден по селектору: ${selector}`);
        }
        if (index >= elements.length) {
          throw new Error(`Индекс ${index} превышает количество найденных элементов (${elements.length})`);
        }
        element = elements[index];
      } else {
        throw new Error('Не указан селектор или координаты для клика');
      }

      // Проверяем, что элемент видим и доступен для клика
      const rect = element.getBoundingClientRect();
      const isVisible = rect.width > 0 && rect.height > 0 && 
                       rect.top >= 0 && rect.left >= 0 &&
                       rect.bottom <= window.innerHeight && 
                       rect.right <= window.innerWidth;

      if (!isVisible) {
        // Прокручиваем к элементу, если он не видим
        element.scrollIntoView({ behavior: 'smooth', block: 'center' });
        await new Promise(resolve => setTimeout(resolve, 500)); // Ждем завершения прокрутки
      }

      // Выполняем клик
      const clickEvent = new MouseEvent('click', {
        bubbles: true,
        cancelable: true,
        view: window
      });

      element.dispatchEvent(clickEvent);

      return {
        success: true,
        element: {
          tagName: element.tagName,
          className: element.className,
          id: element.id,
          text: element.textContent?.trim() || '',
          selector: selector,
          coordinates: coordinates
        },
        timestamp: new Date().toISOString()
      };
    } catch (error) {
      throw new Error(`Ошибка клика по элементу: ${error.message}`);
    }
  }

  async inputData(params) {
    try {
      const { selector, value, index = 0, clear = true } = params;

      if (!selector) {
        throw new Error('Селектор не указан');
      }

      if (value === undefined || value === null) {
        throw new Error('Значение для ввода не указано');
      }

      const elements = document.querySelectorAll(selector);
      if (elements.length === 0) {
        throw new Error(`Элемент не найден по селектору: ${selector}`);
      }

      if (index >= elements.length) {
        throw new Error(`Индекс ${index} превышает количество найденных элементов (${elements.length})`);
      }

      const element = elements[index];

      // Проверяем, что элемент может принимать ввод
      const inputTypes = ['INPUT', 'TEXTAREA', 'SELECT'];
      if (!inputTypes.includes(element.tagName)) {
        throw new Error(`Элемент ${element.tagName} не поддерживает ввод данных`);
      }

      // Фокусируемся на элементе
      element.focus();

      // Очищаем поле, если требуется
      if (clear) {
        element.value = '';
      }

      // Вводим данные
      if (element.tagName === 'SELECT') {
        // Для select элементов ищем option с соответствующим значением
        const option = Array.from(element.options).find(opt => 
          opt.value === value || opt.text === value
        );
        if (option) {
          element.selectedIndex = option.index;
        } else {
          throw new Error(`Опция "${value}" не найдена в select элементе`);
        }
      } else {
        // Для input и textarea
        element.value = value;
      }

      // Генерируем события для имитации пользовательского ввода
      const inputEvent = new Event('input', { bubbles: true });
      const changeEvent = new Event('change', { bubbles: true });
      
      element.dispatchEvent(inputEvent);
      element.dispatchEvent(changeEvent);

      return {
        success: true,
        element: {
          tagName: element.tagName,
          className: element.className,
          id: element.id,
          type: element.type || '',
          value: element.value,
          selector: selector
        },
        inputValue: value,
        timestamp: new Date().toISOString()
      };
    } catch (error) {
      throw new Error(`Ошибка ввода данных: ${error.message}`);
    }
  }

  // Вспомогательный метод для получения информации о странице
  getPageInfo() {
    return {
      url: window.location.href,
      title: document.title,
      readyState: document.readyState,
      timestamp: new Date().toISOString(),
      viewport: {
        width: window.innerWidth,
        height: window.innerHeight
      },
      scroll: {
        x: window.scrollX,
        y: window.scrollY
      }
    };
  }
}

// Инициализируем DOMInteractor
const domInteractor = new DOMInteractor();

// Отправляем сообщение о готовности content script
chrome.runtime.sendMessage({
  type: 'CONTENT_SCRIPT_READY',
  pageInfo: domInteractor.getPageInfo()
}).catch(error => {
  console.log('Background script не готов:', error);
});

console.log('Content script загружен и готов к работе');