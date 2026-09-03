Unicode true

####
## Please note: Template replacements don't work in this file. They are provided with default defines like
## mentioned underneath.
## If the keyword is not defined, "wails_tools.nsh" will populate them with the values from ProjectInfo.
## If they are defined here, "wails_tools.nsh" will not touch them. This allows to use this project.nsi manually
## from outside of Wails for debugging and development of the installer.
##
## For development first make a wails nsis build to populate the "wails_tools.nsh":
## > wails build --target windows/amd64 --nsis
## Then you can call makensis on this file with specifying the path to your binary:
## For a AMD64 only installer:
## > makensis -DARG_WAILS_AMD64_BINARY=..\..\bin\app.exe
## For a ARM64 only installer:
## > makensis -DARG_WAILS_ARM64_BINARY=..\..\bin\app.exe
## For a installer with both architectures:
## > makensis -DARG_WAILS_AMD64_BINARY=..\..\bin\app-amd64.exe -DARG_WAILS_ARM64_BINARY=..\..\bin\app-arm64.exe
####
## The following information is taken from the ProjectInfo file, but they can be overwritten here.
####
## !define INFO_PROJECTNAME    "MyProject" # Default "{{.Name}}"
## !define INFO_COMPANYNAME    "MyCompany" # Default "{{.Info.CompanyName}}"
## !define INFO_PRODUCTNAME    "MyProduct" # Default "{{.Info.ProductName}}"
## !define INFO_PRODUCTVERSION "1.0.0"     # Default "{{.Info.ProductVersion}}"
## !define INFO_COPYRIGHT      "Copyright" # Default "{{.Info.Copyright}}"
###
## !define PRODUCT_EXECUTABLE  "Application.exe"      # Default "${INFO_PROJECTNAME}.exe"
## !define UNINST_KEY_NAME     "UninstKeyInRegistry"  # Default "${INFO_COMPANYNAME}${INFO_PRODUCTNAME}"
####
## !define REQUEST_EXECUTION_LEVEL "admin"            # Default "admin"  see also https://nsis.sourceforge.io/Docs/Chapter4.html
####
## Include the wails tools
####
!include "wails_tools.nsh"

# The version information for this two must consist of 4 parts.
#
# NeoBox's version is three parts (1.8.0), so — like the stock Wails
# template — ".0" is appended here to make four. If the version ever grows
# a fourth part of its own (e.g. 1.8.0.1), drop this ".0" instead, since
# five parts makes makensis reject the build.
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

# Enable HiDPI support. https://nsis.sourceforge.io/Reference/ManifestDPIAware
ManifestDPIAware true

!include "MUI.nsh"

; Fix: явно задаём стандартный шрифт Windows чтобы избежать «билеберды» на экране запуска
!define MUI_FONT "Segoe UI"
!define MUI_FONT_SIZE 9

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
# !define MUI_WELCOMEFINISHPAGE_BITMAP "resources\leftimage.bmp" #Include this to add a bitmap on the left side of the Welcome Page. Must be a size of 164x314
# !define MUI_FINISHPAGE_NOAUTOCLOSE # Wait on the INSTFILES page so the user can take a look into the details of the installation steps
!define MUI_ABORTWARNING # This will warn the user if they exit from the installer.

!insertmacro MUI_PAGE_WELCOME # Welcome to the installer page.
# !insertmacro MUI_PAGE_LICENSE "resources\eula.txt" # Adds a EULA page to the installer
!insertmacro MUI_PAGE_DIRECTORY # In which folder install page.
!insertmacro MUI_PAGE_INSTFILES # Installing page.

!define MUI_FINISHPAGE_RUN "$INSTDIR\${PRODUCT_EXECUTABLE}"
!define MUI_FINISHPAGE_RUN_TEXT "Launch NeoBox"
!insertmacro MUI_PAGE_FINISH # Finished installation page.

!insertmacro MUI_UNPAGE_INSTFILES # Uinstalling page

!insertmacro MUI_LANGUAGE "English" # Set the Language of the installer
!insertmacro MUI_LANGUAGE "Russian"

LangString LaunchApp ${LANG_ENGLISH} "Launch NeoBox"
LangString LaunchApp ${LANG_RUSSIAN} "Запустить NeoBox"

; Вопрос про данные при удалении. Текст, а не «мы всё стёрли»: подписки — это
; чужие ссылки, которые человек собирал руками, и второй раз он их не соберёт.
LangString RemoveUserData ${LANG_ENGLISH} "Also delete your settings, subscriptions and history?$\n$\nChoose No to keep them: installing NeoBox again will pick them up where they were."
LangString RemoveUserData ${LANG_RUSSIAN} "Удалить также настройки, подписки и историю?$\n$\nЕсли выбрать «Нет», они останутся на месте: следующая установка NeoBox подхватит их обратно."


## The following two statements can be used to sign the installer and the uninstaller. The path to the binaries are provided in %1
#!uninstfinalize 'signtool --file "%1"'
#!finalize 'signtool --file "%1"'
!finalize 'cmd /c copy "%1" "..\..\..\"'

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\bin\${INFO_PROJECTNAME}_Setup_v${INFO_PRODUCTVERSION}.exe" # Name of the installer's file.
InstallDir "$PROGRAMFILES64\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}" # Default installing folder ($PROGRAMFILES is Program Files folder).
ShowInstDetails show # This will always show the installation details.

Function .onInit
   !insertmacro wails.checkArchitecture

   ; Подставить в страницу выбора каталога тот путь, куда NeoBox уже
   ; установлен, вместо значения InstallDir по умолчанию.
   ;
   ; Без этого обновление у человека, выбравшего когда-то свой каталог,
   ; уходило по стандартному пути: снятие прежней версии работает по
   ; $INSTDIR, то есть промахивалось, и рядом оставалась вторая копия
   ; вместе со своим ярлыком.
   ;
   ; SetRegView 64 обязателен и стоит здесь, а не в InstallDirRegKey.
   ; Установщик 32-разрядный, его вид реестра по умолчанию тоже, и запись
   ; об установке искалась бы в Wow6432Node — где её нет: wails.writeUninstaller
   ; пишет её под 64-разрядным видом. А InstallDirRegKey читается движком
   ; на старте, до .onInit, так что повлиять на него отсюда нельзя вовсе.
   SetRegView 64
   ReadRegStr $0 HKLM "${UNINST_KEY}" "InstallLocation"
   StrCmp $0 "" 0 use_previous_dir
   ReadRegStr $0 HKCU "${UNINST_KEY}" "InstallLocation"
   StrCmp $0 "" 0 use_previous_dir

   ; Записи, оставленные версиями до этой, поля InstallLocation не имеют:
   ; его никто не писал. Для них каталог выводится из пути к деинсталлятору
   ; — иначе первое же обновление промахнулось бы мимо нестандартной
   ; установки, то есть ровно там, где это и нужно.
   ReadRegStr $0 HKLM "${UNINST_KEY}" "UninstallString"
   StrCmp $0 "" 0 derive_from_uninstaller
   ReadRegStr $0 HKCU "${UNINST_KEY}" "UninstallString"
   StrCmp $0 "" oninit_done

derive_from_uninstaller:
   ; Путь записан в кавычках — снять их, иначе GetParent разберёт мусор.
   StrCpy $1 $0 1
   StrCmp $1 '"' 0 +2
   StrCpy $0 $0 -1 1
   ${GetParent} "$0" $1
   StrCpy $0 "$1"

use_previous_dir:
   ; Каталог мог быть удалён руками — тогда прежний путь не подсказка.
   IfFileExists "$0\*.*" 0 oninit_done
   StrCpy $INSTDIR "$0"

oninit_done:
FunctionEnd

Section
    !insertmacro wails.setShellContext

    # Kill any running instances to avoid locked files during installation
    DetailPrint "Closing running instances of NeoBox and sing-box..."
    nsExec::Exec "taskkill /F /IM NeoBox-Go.exe"
    nsExec::Exec "taskkill /F /IM NeoBox.exe"
    nsExec::Exec "taskkill /F /IM sing-box.exe"
    Sleep 1000

    # 1. Предыдущая версия на Go: удаляется САМА ПРОГРАММА, файл за файлом.
    #
    #    Здесь стоял симметричный блок, который читал UninstallString ключа
    #    DvaraisNeoBox и запускал "uninstall.exe /S". Этот деинсталлятор ниже
    #    по файлу делает RMDir /r "$APPDATA\NeoBox", то есть тихий прогон
    #    перед установкой уносил settings.json, state.json, subscriptions.json,
    #    history.json и key.bin — подписки, избранное, профили и историю. А
    #    следом стояла приписка «НЕ удаляем $APPDATA\NeoBox здесь, данные
    #    должны сохраняться при обновлении»: сохранять было уже нечего. Тем же
    #    путём идёт встроенный автообновлятор, так что каждое обновление из
    #    приложения обнуляло установку.
    #
    #    Поэтому старая версия снимается напрямую: перечисленным ниже и
    #    исчерпывается всё, что установщик когда-либо клал в каталог. Тот же
    #    результат, что от деинсталлятора, но код, который трогает данные
    #    пользователя, при обновлении не выполняется вовсе — а не полагается на
    #    ключ вроде /KEEPDATA, которого деинсталляторы уже установленных версий
    #    всё равно не понимают.
    #
    #    Процессы погашены выше, поэтому файлы не заняты. Ярлыки и запись в
    #    «Установка и удаление программ» пересоздаются ниже по тем же путям.
    DetailPrint "Removing the previous version..."
    Delete "$INSTDIR\NeoBox.exe"
    # Имя, под которым выходили прежние сборки. Установщик его только
    # taskkill'ил и никогда не удалял, поэтому у всех, кто ставил ту версию, в
    # каталоге до сих пор лежат 35 МБ мёртвого кода.
    Delete "$INSTDIR\NeoBox-Go.exe"
    Delete "$INSTDIR\wintun.dll"
    # Иконка для уведомлений: прежние сборки писали её рядом с exe, теперь она
    # живёт в каталоге данных.
    Delete "$INSTDIR\icon.ico"
    Delete "$INSTDIR\uninstall.exe"

    # 2. Detect and silently uninstall the old Electron version of NeoBox (AppID: com.neobox.vpn)
    ReadRegStr $0 HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\com.neobox.vpn" "UninstallString"
    StrCmp $0 "" check_electron_hkcu
    Goto do_uninstall_electron

check_electron_hkcu:
    ReadRegStr $0 HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\com.neobox.vpn" "UninstallString"
    StrCmp $0 "" end_uninstall_detection

do_uninstall_electron:
    DetailPrint "Uninstalling legacy Electron version of NeoBox..."
    ExecWait '"$0" /S'
    Sleep 1000

end_uninstall_detection:
    # SetRegView стоял в снятом выше блоке и выполнялся всегда, поэтому переехал
    # сюда: без него DeleteRegValue HKLM ниже уходит в Wow6432Node — установщик
    # 32-разрядный — и ключ автозапуска Electron-версии остался бы на месте.
    SetRegView 64

    # Clean up legacy Electron installation folder (NOT user data in APPDATA\NeoBox)
    SetShellVarContext current
    RMDir /r "$LOCALAPPDATA\Programs\neobox"

    ; Fix: удаляем ключи автозапуска Electron-версии NeoBox из реестра
    ; чтобы при старте системы не открывалась старая версия
    DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "NeoBox"
    DeleteRegValue HKLM "Software\Microsoft\Windows\CurrentVersion\Run" "NeoBox"
    DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "neobox"
    DeleteRegValue HKLM "Software\Microsoft\Windows\CurrentVersion\Run" "neobox"

    ; Fix: НЕ удаляем $APPDATA\NeoBox здесь — пользовательские данные
    ; (settings.json, subscriptions.json) должны сохраняться при обновлении!
    ; Удаление пользовательских данных происходит ТОЛЬКО при полной деинсталляции (см. ниже).
    !insertmacro wails.setShellContext

    !insertmacro wails.webview2runtime

    SetOutPath $INSTDIR

    !insertmacro wails.files
    ; Из корня репозитория, а не из build\bin: build/bin целиком в .gitignore,
    ; и никакой шаг сборки wintun.dll туда не кладёт. На этой машине файл там
    ; оказался руками когда-то давно, а на чистом клоне makensis падал на
    ; "could not find file". Отслеживается git'ом ровно одна копия — эта.
    File "..\..\..\wintun.dll"

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"

    !insertmacro wails.associateFiles
    !insertmacro wails.associateCustomProtocols

    !insertmacro wails.writeUninstaller

    ; Куда установлено — отдельным значением. Штатное поле записи в
    ; «Установка и удаление программ», которого wails.writeUninstaller не
    ; пишет; отсюда его читает .onInit следующей версии, чтобы обновление
    ; легло поверх этой установки, а не рядом с ней.
    SetRegView 64
    WriteRegStr HKLM "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
SectionEnd

Section "uninstall"
    !insertmacro wails.setShellContext

    # Kill any running instances before trying to delete files
    nsExec::Exec "taskkill /F /IM NeoBox-Go.exe"
    nsExec::Exec "taskkill /F /IM NeoBox.exe"
    nsExec::Exec "taskkill /F /IM sing-box.exe"
    Sleep 1000

    # Remove shortcuts
    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

    # Remove Wails-specific file associations and protocols
    !insertmacro wails.unassociateFiles
    !insertmacro wails.unassociateCustomProtocols

    # Данные пользователя удаляются только по его слову.
    #
    # Раньше «Удалить» уносило подписки, избранное, профили и историю молча, и
    # человек узнавал об этом после переустановки. Переустановка ради починки —
    # обычное действие, а собранный руками список серверов вторым заходом не
    # восстанавливается ниоткуда: экспорт настроек подписки не переносит
    # намеренно (см. backend/service/transfer.go).
    #
    # /SD IDNO — ответ при тихом прогоне (uninstall.exe /S). Тихое удаление
    # запускает чужой код, и стирать по своей инициативе он не должен.
    # По умолчанию выделена кнопка «Нет» по той же причине.
    SetShellVarContext current
    MessageBox MB_YESNO|MB_ICONQUESTION|MB_DEFBUTTON2 "$(RemoveUserData)" /SD IDNO IDNO keep_user_data
    DetailPrint "Removing settings, subscriptions and history..."
    RMDir /r "$APPDATA\NeoBox"
    RMDir /r "$APPDATA\NeoBox-Go"
    RMDir /r "$LOCALAPPDATA\NeoBox"
    RMDir /r "$LOCALAPPDATA\NeoBox-Go"

keep_user_data:
    # Каталог программы Electron-версии — не данные, уходит всегда.
    RMDir /r "$LOCALAPPDATA\Programs\neobox"
    
    # Restore shell context to all if needed
    !insertmacro wails.setShellContext

    # Remove the installation directory completely
    RMDir /r $INSTDIR

    # Clean up registry keys for both old Electron and Wails versions to ensure no broken Add/Remove entries
    SetRegView 64
    DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\com.neobox.vpn"
    DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\com.neobox.vpn"
    DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\DvaraisNeoBox"
    
    !insertmacro wails.deleteUninstaller
SectionEnd

