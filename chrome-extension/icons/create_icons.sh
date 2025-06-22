#!/bin/bash

# Скрипт для создания PNG иконок из SVG

echo "Создание PNG иконок из SVG файла..."

# Проверяем наличие ImageMagick
if command -v convert &> /dev/null; then
    echo "Используем ImageMagick для конвертации..."
    convert icon.svg -resize 16x16 icon16.png
    convert icon.svg -resize 48x48 icon48.png
    convert icon.svg -resize 128x128 icon128.png
    echo "Иконки созданы успешно!"
elif command -v inkscape &> /dev/null; then
    echo "Используем Inkscape для конвертации..."
    inkscape icon.svg -w 16 -h 16 -o icon16.png
    inkscape icon.svg -w 48 -h 48 -o icon48.png
    inkscape icon.svg -w 128 -h 128 -o icon128.png
    echo "Иконки созданы успешно!"
else
    echo "Ошибка: Не найден ImageMagick или Inkscape"
    echo "Установите один из них:"
    echo "  sudo apt-get install imagemagick"
    echo "  sudo apt-get install inkscape"
    echo ""
    echo "Или используйте онлайн конвертер:"
    echo "  https://convertio.co/svg-png/"
    exit 1
fi

echo "Созданные файлы:"
ls -la *.png