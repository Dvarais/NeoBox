export async function fetchIP(currentIpElement, t, retryCount = 5) {
  currentIpElement.textContent = t.ipDetermining;
  for (let i = 0; i < retryCount; i++) {
    try {
      const controller = new AbortController();
      const timeoutId = setTimeout(() => controller.abort(), 5000);
      
      const res = await fetch('https://api.ipify.org?format=json', { 
        signal: controller.signal,
        cache: 'no-store'
      });
      clearTimeout(timeoutId);
      
      const data = await res.json();
      if (data && data.ip) {
        currentIpElement.textContent = data.ip;
        return;
      }
    } catch (e) {
      console.error(`IP fetch attempt ${i+1} failed:`, e);
      if (i < retryCount - 1) {
        await new Promise(res => setTimeout(res, 3000)); // Ждем 3 сек
      }
    }
  }
  currentIpElement.textContent = t.ipError;
}

export function showPrompt(overlayId, titleId, inputId, cancelBtnId, confirmBtnId, title, defaultValue = '') {
  return new Promise((resolve) => {
    const overlay = document.getElementById(overlayId);
    const titleEl = document.getElementById(titleId);
    const inputEl = document.getElementById(inputId);
    const cancelBtn = document.getElementById(cancelBtnId);
    const confirmBtn = document.getElementById(confirmBtnId);

    titleEl.innerText = title;
    inputEl.value = defaultValue === 'OPEN_LINK' ? '' : defaultValue;
    inputEl.style.display = defaultValue === 'OPEN_LINK' ? 'none' : 'block';
    
    overlay.style.display = 'flex';
    inputEl.focus();
    inputEl.select();

    const cleanup = () => {
      overlay.style.display = 'none';
      confirmBtn.onclick = null;
      cancelBtn.onclick = null;
      inputEl.onkeydown = null;
    };

    confirmBtn.onclick = () => {
      const val = inputEl.value;
      cleanup();
      resolve(val);
    };

    cancelBtn.onclick = () => {
      cleanup();
      resolve(null);
    };

    inputEl.onkeydown = (e) => {
      if (e.key === 'Enter') confirmBtn.click();
      if (e.key === 'Escape') cancelBtn.click();
    };
  });
}

export function showConfirm(overlayId, titleId, inputId, cancelBtnId, confirmBtnId, title) {
  return new Promise((resolve) => {
    const overlay = document.getElementById(overlayId);
    const titleEl = document.getElementById(titleId);
    const inputEl = document.getElementById(inputId);
    const cancelBtn = document.getElementById(cancelBtnId);
    const confirmBtn = document.getElementById(confirmBtnId);

    titleEl.innerText = title;
    inputEl.style.display = 'none';
    
    overlay.style.display = 'flex';
    
    const cleanup = () => {
      overlay.style.display = 'none';
      confirmBtn.onclick = null;
      cancelBtn.onclick = null;
    };

    confirmBtn.onclick = () => {
      cleanup();
      resolve(true);
    };

    cancelBtn.onclick = () => {
      cleanup();
      resolve(false);
    };
  });
}

export function showAlert(title, message, isError = false, t = {}) {
  return new Promise((resolve) => {
    const overlay = document.getElementById('alertOverlay');
    const titleEl = document.getElementById('alertTitleText');
    const msgEl = document.getElementById('alertMessageText');
    const termContainer = document.getElementById('errorTerminalContainer');
    const termText = document.getElementById('errorTerminalText');
    const copyBtn = document.getElementById('alertCopyBtn');
    const copyBtnText = document.getElementById('alertCopyBtnText');
    const confirmBtn = document.getElementById('alertConfirmBtn');

    titleEl.innerText = title;
    
    // Style icon or container according to isError
    const iconContainer = overlay.querySelector('.alert-icon-container');
    if (isError) {
      overlay.classList.add('error-mode');
      if (iconContainer) iconContainer.classList.add('error');
    } else {
      overlay.classList.remove('error-mode');
      if (iconContainer) iconContainer.classList.remove('error');
    }

    // Check if the message is extremely long (like a sing-box initialize error) and put it into the terminal container
    const isLongError = isError && (message.length > 80 || message.includes('\n') || message.includes('failed') || message.includes('uTLS'));
    
    if (isLongError) {
      msgEl.innerText = isError ? (t.errorDialogTitle || 'Ошибка запуска:') : message;
      termText.innerText = message;
      termContainer.style.display = 'block';
    } else {
      msgEl.innerText = message;
      termContainer.style.display = 'none';
      termText.innerText = '';
    }

    // Copy to clipboard setup
    if (isError) {
      copyBtn.style.display = 'flex';
      copyBtnText.innerText = t.errorDialogCopy || 'Копировать';
      copyBtn.onclick = () => {
        navigator.clipboard.writeText(message).then(() => {
          copyBtnText.innerText = t.errorDialogCopied || 'Скопировано!';
          setTimeout(() => {
            copyBtnText.innerText = t.errorDialogCopy || 'Копировать';
          }, 1500);
        });
      };
    } else {
      copyBtn.style.display = 'none';
      copyBtn.onclick = null;
    }

    confirmBtn.innerText = t.errorDialogClose || 'ОК';
    overlay.style.display = 'flex';
    confirmBtn.focus();

    const cleanup = () => {
      overlay.style.display = 'none';
      confirmBtn.onclick = null;
      if (copyBtn) copyBtn.onclick = null;
      document.onkeydown = null;
    };

    confirmBtn.onclick = () => {
      cleanup();
      resolve(true);
    };

    document.onkeydown = (e) => {
      if (e.key === 'Enter' || e.key === 'Escape') {
        confirmBtn.click();
      }
    };
  });
}

