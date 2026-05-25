export let allSubscriptions = [];
export let currentActiveSubId = 'all';

export async function loadSubscriptions(callback) {
  allSubscriptions = await window.api.getSubscriptions();
  if (callback) callback();
}

export function renderSubTabs(container, translations, currentLanguage, onTabChange, showPrompt, showConfirm, loadSubscriptions) {
  if (!container) return;
  const t = translations[currentLanguage];
  container.innerHTML = '';
  
  const createTab = (id, name) => {
    const btn = document.createElement('button');
    btn.className = `btn-glass ${currentActiveSubId === id ? 'active' : ''}`;
    btn.style.padding = '6px 15px';
    btn.style.position = 'relative';
    
    let displayName = id === 'all' ? t.allServersTab : name;
    if (id !== 'all') {
      const sub = allSubscriptions.find(s => s.id === id);
      if (sub && sub.loading) {
        displayName += ' ⏳';
      }
    }
    btn.textContent = displayName;
    
    btn.onclick = (e) => {
      currentActiveSubId = id;
      onTabChange();
    };

    if (id !== 'all') {
      btn.draggable = true;
      
      btn.ondragstart = (e) => {
        e.dataTransfer.setData('text/plain', id);
        btn.classList.add('dragging');
        btn.style.opacity = '0.5';
      };

      btn.ondragend = (e) => {
        btn.classList.remove('dragging');
        btn.style.opacity = '1';
      };

      btn.ondragover = (e) => {
        e.preventDefault();
      };

      btn.ondrop = async (e) => {
        e.preventDefault();
        const draggedId = e.dataTransfer.getData('text/plain');
        if (draggedId && draggedId !== id && draggedId !== 'all') {
          const draggedIndex = allSubscriptions.findIndex(s => s.id === draggedId);
          const targetIndex = allSubscriptions.findIndex(s => s.id === id);
          if (draggedIndex !== -1 && targetIndex !== -1) {
            const [draggedSub] = allSubscriptions.splice(draggedIndex, 1);
            allSubscriptions.splice(targetIndex, 0, draggedSub);
            await window.api.saveSubscriptions(allSubscriptions);
            await loadSubscriptions(() => {
              renderSubTabs(container, translations, currentLanguage, onTabChange, showPrompt, showConfirm, loadSubscriptions);
              onTabChange();
            });
          }
        }
      };

      btn.oncontextmenu = (e) => {
        e.preventDefault();
        document.querySelectorAll('.tab-context-menu').forEach(m => m.remove());
        
        const menu = document.createElement('div');
        menu.className = 'tab-context-menu';
        menu.style.position = 'fixed';
        menu.style.top = `${e.clientY}px`;
        menu.style.left = `${e.clientX}px`;
        menu.style.background = 'var(--card-bg)';
        menu.style.border = '1px solid var(--glass-border)';
        menu.style.borderRadius = '8px';
        menu.style.padding = '5px';
        menu.style.zIndex = '1000';
        menu.style.boxShadow = '0 10px 25px rgba(0,0,0,0.5)';
        
        const renameItem = document.createElement('div');
        renameItem.textContent = t.renameItem || '✏️ Rename';
        renameItem.style.padding = '8px 12px';
        renameItem.style.cursor = 'pointer';
        renameItem.style.fontSize = '13px';
        renameItem.style.borderRadius = '4px';
        renameItem.onmouseover = () => renameItem.style.background = 'rgba(255, 255, 255, 0.05)';
        renameItem.onmouseout = () => renameItem.style.background = 'transparent';
        
        renameItem.onclick = async () => {
          menu.remove();
          const newName = await showPrompt(t.renamePromptTitle || 'Rename subscription', name);
          if (newName && newName.trim() !== '') {
            const sub = allSubscriptions.find(s => s.id === id);
            if (sub) {
              sub.name = newName.trim();
              await window.api.saveSubscriptions(allSubscriptions);
              await loadSubscriptions(() => {
                renderSubTabs(container, translations, currentLanguage, onTabChange, showPrompt, showConfirm, loadSubscriptions);
                onTabChange();
              });
            }
          }
        };

        const deleteItem = document.createElement('div');
        deleteItem.textContent = t.deleteItem || '🗑 Delete subscription';
        deleteItem.style.padding = '8px 12px';
        deleteItem.style.cursor = 'pointer';
        deleteItem.style.fontSize = '13px';
        deleteItem.style.borderRadius = '4px';
        deleteItem.style.color = 'var(--danger)';
        
        deleteItem.onmouseover = () => deleteItem.style.background = 'rgba(239, 68, 68, 0.1)';
        deleteItem.onmouseout = () => deleteItem.style.background = 'transparent';
        
        deleteItem.onclick = async () => {
          menu.remove();
          const confirmed = await showConfirm(t.deleteConfirm.replace('{name}', name));
          if (confirmed) {
            allSubscriptions = allSubscriptions.filter(s => s.id !== id);
            await window.api.saveSubscriptions(allSubscriptions);
            if (currentActiveSubId === id) currentActiveSubId = 'all';
            await loadSubscriptions(() => {
              renderSubTabs(container, translations, currentLanguage, onTabChange, showPrompt, showConfirm, loadSubscriptions);
              onTabChange();
            });
          }
        };
        
        menu.appendChild(renameItem);
        menu.appendChild(deleteItem);
        document.body.appendChild(menu);
        
        const closeMenu = () => {
          menu.remove();
          document.removeEventListener('click', closeMenu);
        };
        setTimeout(() => document.addEventListener('click', closeMenu), 10);
      };
    }

    return btn;
  };

  container.appendChild(createTab('all', t.allServersTab));
  allSubscriptions.forEach(sub => {
    container.appendChild(createTab(sub.id, sub.name));
  });
}

export function setActiveSubId(id) {
    currentActiveSubId = id;
}

export function setSubscriptions(subs) {
    allSubscriptions = subs;
}
