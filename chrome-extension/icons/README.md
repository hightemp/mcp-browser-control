# Иконки расширения

Для полноценной работы расширения необходимо создать PNG иконки следующих размеров:

- icon16.png (16x16 пикселей)
- icon48.png (48x48 пикселей) 
- icon128.png (128x128 пикселей)

Можно использовать базовую SVG иконку (icon.svg) для создания PNG версий.

## Создание PNG иконок из SVG

Вы можете использовать онлайн конвертеры или инструменты командной строки:

### С помощью ImageMagick:
```bash
# Установка ImageMagick (если не установлен)
sudo apt-get install imagemagick

# Конвертация SVG в PNG разных размеров
convert icon.svg -resize 16x16 icon16.png
convert icon.svg -resize 48x48 icon48.png  
convert icon.svg -resize 128x128 icon128.png
```

### С помощью Inkscape:
```bash
# Установка Inkscape (если не установлен)
sudo apt-get install inkscape

# Конвертация SVG в PNG
inkscape icon.svg -w 16 -h 16 -o icon16.png
inkscape icon.svg -w 48 -h 48 -o icon48.png
inkscape icon.svg -w 128 -h 128 -o icon128.png
```

### Онлайн конвертеры:
- https://convertio.co/svg-png/
- https://cloudconvert.com/svg-to-png
- https://www.freeconvert.com/svg-to-png

## Временное решение

Для тестирования расширения можно временно использовать любые PNG файлы соответствующих размеров или создать простые цветные квадраты.