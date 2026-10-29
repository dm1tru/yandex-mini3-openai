# Yandex Mini 3 + OpenAI

Голосовой помощник на Яндекс Станции **Мини 3 (YNDX-00027, Bergamot)**: локальное распознавание Vosk, ответы OpenAI и синтез речи OpenAI. VPN WireGuard работает непосредственно на колонке.

```text
Микрофон → Vosk → «Компьютер, <вопрос>» → OpenAI Responses
                                                  ↓
Динамик ← ALSA ← PCM 48 kHz stereo ← OpenAI Speech
```

Проверено 29.10.2026 на прошивке `0.369.1.50.4794652521.20261021.117`, ядре Linux 4.9.68 aarch64, 32-битном armhf userspace glibc 2.30 и 238 MiB RAM. Это эксперимент для уже подготовленной колонки с root/SSH; автоматического получения root или перепрошивки здесь нет.

## Что работает

- Обращение **«Компьютер, сколько будет два плюс два?»**.
- Vosk small-ru-0.22 на устройстве, первый микрофонный канал, PDM Gain 36.
- `gpt-4.1-mini` через Responses API; `gpt-4o-mini-tts`, голос `coral`.
- Отправка только текста вопроса после обращения; локальный звук не отправляется в API.
- Захват продолжает работать во время запросов и ответа, но входящий звук отбрасывается, чтобы помощник не отвечал сам себе.
- Запуск/остановка из SSH; возврат штатной Алисы после остановки.
- WireGuard с отдельной таблицей маршрутов и синхронизацией времени до подключения.

Оплата OpenAI API отдельная от подписки ChatGPT. Каждый вопрос и озвучивание расходуют API-баланс. Голос синтезирован ИИ. История диалога, инструменты управления домом и автозапуск помощника пока не реализованы.

## Содержимое

| Файл | Назначение |
| --- | --- |
| `mini3-openai/` | Go-клиент OpenAI, голосовой цикл и тесты |
| `vosk-stream.c` | Потоковый Vosk, захват отдельным потоком, ограничение фрагментов |
| `vosk-transcribe.c`, `vosk-test` | Распознавание WAV и ручная проверка микрофона |
| `gpt-live` | Управление голосовым OpenAI |
| `vosk-live` | Режим локального распознавания без API |
| `S98mini3wg.v2`, `mini3-dhcpcd.exit-hook` | WireGuard и DHCP hook с локальной конфигурацией |
| `config/network.conf.example` | Пример параметров сети без персональных адресов |
| [OPENAI.md](OPENAI.md) | Настройка API, ключей, голоса, протокол проверок |
| [VOSK.md](VOSK.md) | Формат аудио, измерения, диагностика |
| [VPN-REPAIR.md](VPN-REPAIR.md) | Причины сбоев VPN и проверенное исправление |
| [docs/DEVICE-PREPARATION.md](docs/DEVICE-PREPARATION.md) | Что было сделано до установки помощника |

Настройки своей сети храните отдельно от Git: скопируйте `config/network.conf.example` в `/data/opt/etc/wireguard/network.conf` и замените все демонстрационные адреса. Подробности — в [VPN-REPAIR.md](VPN-REPAIR.md). Сетевой скрипт без заполненного локального конфига не запускается. Его установка не требуется, если VPN уже работает.

## Сборка (Linux или WSL с Docker)

```sh
sh build.sh
sh fetch-model.sh
```

В `dist/` появятся `mini3-openai` (статический aarch64), `vosk-stream`, `vosk-transcribe` (armhf) и `libvosk.so`. В `models/` — модель. Сборка выполняет Go-тесты. Vosk скачивается из официального релиза с проверкой SHA256. Бинарники, архивы модели и приватные файлы в Git не хранятся.

Для установки передайте файлы по SSH/SCP с учётом поддержки SCP/SFTP вашей сборкой Dropbear:

```text
dist/mini3-openai       → /data/opt/bin/mini3-openai
dist/vosk-stream        → /data/opt/bin/vosk-stream
dist/vosk-transcribe    → /data/opt/bin/vosk-transcribe
dist/libvosk.so         → /data/opt/vosk/lib/libvosk.so
gpt-live               → /data/opt/bin/gpt-live
vosk-live              → /data/opt/bin/vosk-live
vosk-test              → /data/opt/bin/vosk-test
models/vosk-model-small-ru-0.22/ → /data/opt/vosk/model-ru/
```

Исполняемым файлам нужны права 755. Ключ OpenAI разместите отдельно в `/data/opt/openai/key.txt`: каталог 700, файл 600. Не вставляйте ключ в код и не коммитьте его. Нужны работающий интернет через VPN, корректное время и системные CA. Проверьте наличие `/dev/zram0`: управляющий скрипт включает 160 MiB сжатого swap в RAM, без записи на флеш.

## Запуск

```sh
/data/opt/bin/gpt-live start
tail -f /tmp/gpt-live.log
```

После `ready` подождите секунду и произнесите вопрос с обращением «Компьютер». `Ctrl+C` закрывает просмотр журнала, но помощник продолжает работать.

```sh
/data/opt/bin/gpt-live status
/data/opt/bin/gpt-live stop
```

Громкость ответов:

```sh
amixer sset 'Master' 50%
```

`PDM Gain` отвечает за микрофон, `Master` — за динамик.

## Проверки и ограничения

Проверена полная цепочка: произнесённый через динамик тестовый вопрос распознан микрофоном, получен ответ OpenAI и воспроизведён. Пользователь также подтвердил, что всё работает. Проверены остановка сервиса, возврат Алисы и повторный запуск.

В раннем варианте потокового Vosk был OOM и `overrun`. Добавлены сброс распознавателя после фраз и ограничение до 15 секунд аудио. Драйвер ограничивает ALSA-буфер примерно 43 мс, поэтому запрос увеличенного буфера сам по себе проблему не решает. Короткие проверки после исправления прошли, длительная стабильность остаётся предметом наблюдения. Нет гарантии работы на других моделях и прошивках.

## Источники

Архитектура изучена по [Yandex-Claude-Assistant](https://github.com/alexrosette/Yandex-Claude-Assistant), commit `4dc56d84ace49587094ffe2aef7465601085d587` (MIT). Там используются Claude Messages API и ElevenLabs; здесь написан отдельный Go-клиент OpenAI. Прямые записи в регистры усилителя из исходного проекта не применяются.

- [Vosk](https://alphacephei.com/vosk/) — библиотека и русская модель; сторонние компоненты сохраняют свои лицензии.
- [OpenAI Responses](https://developers.openai.com/api/docs/guides/text), [Speech API](https://developers.openai.com/api/docs/guides/text-to-speech).
- [hackalice.me](https://hackalice.me/) — подготовка устройства.
- [yandex-station-extended](https://github.com/Marker284/yandex-station-extended) — использованный отдельно скрипт отключения OTA.

Собственный код и документация распространяются по [MIT](LICENSE). Лицензии сторонних компонентов перечислены в [THIRD_PARTY.md](THIRD_PARTY.md).

Перед отправкой issue или pull request прочитайте [CONTRIBUTING.md](CONTRIBUTING.md) и [SECURITY.md](SECURITY.md): ключи, конфигурация VPN, адреса личных серверов и записи голоса не должны попадать в публичные материалы.

Проект не связан с Яндексом, OpenAI или авторами перечисленных проектов.
