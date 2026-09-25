# Выявленные и исправленные баги

### 1. Краш в трее при сбросе видеодрайвера (TDR)
* **Симптом:** Приложение внезапно бесследно исчезает из трея.
* **Причина:** При сбросе видеодрайвера (`LiveKernelEvent 141 / AMD_WATCHDOG`) падал процесс рендерера WebView2 (`kind 0`). Внутренний обработчик Wails при этом принудительно вызывает `os.Exit(-1)`.
* **Решение:** В [`main.go`](file:///C:/Users/tik26/Desktop/NeoBox-Go/main.go) для `windows.Options` включён флаг `WebviewGpuIsDisabled: true` (отключение аппаратного 3D-ускорения для 2D-интерфейса) и явно задан `WebviewUserDataPath: userDataDir`.

### 2. Рассинхронизация Single-Instance мьютекса (`Global\` vs `Local\`)
* **Симптом:** Параллельный запуск двух экземпляров приложения (обычного и от администратора).
* **Причина:** Непривилегированный процесс не мог создать `Global\` мьютекс и создавал `Local\`. Привилегированный экземпляр создавал `Global\` и не проверял `Local\`.
* **Решение:** В [`backend/service/elevation.go`](file:///C:/Users/tik26/Desktop/NeoBox-Go/backend/service/elevation.go) добавлен пермиссивный DACL `D:(A;;GA;;;WD)(A;;GA;;;AU)`, а `AcquireSingleInstanceMutex` проверяет оба пространства имён (`Global\` и `Local\`) через `OpenMutex(SYNCHRONIZE)` перед захватом.

### 3. Ложное убийство живого WebView2 при старте (`SweepOrphanedWebViews`)
* **Симптом:** Падение или зависание сессии при перезапуске/релонче.
* **Причина:** `SweepOrphanedWebViews` завершал `msedgewebview2.exe` по пути к каталогу профиля, не проверяя, жив ли родительский процесс `NeoBox.exe`.
* **Решение:** В [`backend/service/webview_children.go`](file:///C:/Users/tik26/Desktop/NeoBox-Go/backend/service/webview_children.go) функция `processTree` дополнена картой родителей; если родительский процесс жив и называется `NeoBox.exe`, его WebView2 не завершается.

### 4. Access Violation (0xc0000005) в Windows CryptoAPI при проверке сертификатов
* **Симптом:** Фатальный сбой рантайма Go внутри `crypt32.dll` (`CertFreeCertificateChain`).
* **Причина:** Системный верификатор `crypto/x509` в Windows периодически падает при конкурентных TLS-хэндшейках.
* **Решение:** Первая попытка (`RootCAs = x509.SystemCertPool()` в `backend/core/tls.go`) не работала: на Windows этот пул — маркер, и `Verify` всё равно уходит в `crypt32.dll`. Теперь [`backend/core/roots_windows.go`](backend/core/roots_windows.go) один раз при старте собирает корни из хранилища Windows `ROOT` и бандла Mozilla и ставит их через `x509.SetFallbackRoots`, а `//go:debug x509usefallbackroots=1` в `main.go` подменяет ими системный пул для всего процесса, включая sing-box. Проверка: `TestSystemRootsBypassCryptoAPI`.

### 5. Неперехваченные паники в фоновых горутинах
* **Симптом:** Закрытие приложения без окон и логов при непредвиденной сетевой/структурной ошибке.
* **Причина:** В фоновых циклах `startTrafficMonitor` и `StartAutoUpdateScheduler` отсутствовал `recover()`.
* **Решение:** В [`backend/service/vpn.go`](file:///C:/Users/tik26/Desktop/NeoBox-Go/backend/service/vpn.go) и [`backend/service/subscriptions.go`](file:///C:/Users/tik26/Desktop/NeoBox-Go/backend/service/subscriptions.go) добавлены защитные блоки `defer func() { if r := recover(); r != nil { ... } }()`.

### 6. Конфликт со старой задачей Планировщика задач
* **Симптом:** Дублирующий запуск приложения от администратора при входе в систему.
* **Причина:** В системе оставалась устаревшая задача `NeoBox-Go`, тогда как приложение перешло на `HKCU\...\Run` и удаляло только `NeoBox`.
* **Решение:** В [`backend/security/scheduler.go`](file:///C:/Users/tik26/Desktop/NeoBox-Go/backend/security/scheduler.go) добавлена функция `RemoveLegacyScheduledTasks()`, удаляющая обе задачи (`NeoBox` и `NeoBox-Go`) при наличии прав администратора.
