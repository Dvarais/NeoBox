export let pingData = {};
export let currentSortMode = 'default';

export function parseBasicInfo(link) {
  try {
    let protocol = '';
    let rest = '';
    if (link.includes('://')) {
      const parts = link.split('://');
      protocol = parts[0].toLowerCase();
      rest = parts[1];
    }

    let name = '';
    let address = '';

    // Handle hash/fragment for name
    if (rest.includes('#')) {
      const hashParts = rest.split('#');
      rest = hashParts[0];
      try {
        name = decodeURIComponent(hashParts[1]) || '';
      } catch (e) {
        name = hashParts[1] || '';
      }
    }

    if (protocol === 'vmess') {
      // For VMess, rest is the base64-encoded string
      try {
        const decoded = window.api.decodeBase64(rest);
        const vmessData = JSON.parse(decoded);
        name = name || vmessData.ps || '';
        address = vmessData.add || '';
      } catch (e) {
        address = 'vmess-config';
      }
    } else if (protocol === 'ss') {
      try {
        const url = new URL(link);
        name = name || decodeURIComponent(url.hash.replace('#', '')) || '';
        address = url.hostname || '';
        if (!url.username && url.hostname && !url.port) {
          const decoded = window.api.decodeBase64(url.hostname);
          if (decoded.includes('@')) {
            address = decoded.split('@')[1].split(':')[0];
          }
        }
      } catch (e) {
        if (rest.includes('@')) {
          address = rest.split('@')[1].split(':')[0];
        } else {
          try {
            const decoded = window.api.decodeBase64(rest);
            if (decoded.includes('@')) {
              address = decoded.split('@')[1].split(':')[0];
            }
          } catch (e2) {}
        }
      }
    } else {
      // standard protocol: vless, trojan, tuic, hysteria2, hy2
      try {
        const url = new URL(link);
        name = name || decodeURIComponent(url.hash.replace('#', '')) || '';
        address = url.hostname || '';
      } catch (e) {
        let hostPort = rest;
        if (hostPort.includes('@')) {
          hostPort = hostPort.split('@')[1];
        }
        if (hostPort.includes('?')) {
          hostPort = hostPort.split('?')[0];
        }
        if (hostPort.includes('/')) {
          hostPort = hostPort.split('/')[0];
        }
        if (hostPort.includes(':')) {
          address = hostPort.split(':')[0];
        } else {
          address = hostPort;
        }
      }
    }

    return {
      type: protocol === 'ss' ? 'ShadowSocks' : protocol,
      name: name,
      address: address
    };
  } catch (e) { 
    return { type: '', name: '', address: 'Неизвестно' }; 
  }
}

export function sortServers(servers, sortMode, pingData) {
  const sorted = [...servers];
  
  if (sortMode === 'ping') {
    sorted.sort((a, b) => {
      const pA = pingData[a] || 9999;
      const pB = pingData[b] || 9999;
      const valA = pA === -1 ? 10000 : pA;
      const valB = pB === -1 ? 10000 : pB;
      return valA - valB;
    });
  } else if (sortMode === 'name') {
    sorted.sort((a, b) => {
      const infoA = parseBasicInfo(a);
      const infoB = parseBasicInfo(b);
      return (infoA.name || infoA.address).localeCompare(infoB.name || infoB.address);
    });
  } else if (sortMode === 'protocol') {
    sorted.sort((a, b) => {
      const infoA = parseBasicInfo(a);
      const infoB = parseBasicInfo(b);
      return infoA.type.localeCompare(infoB.type);
    });
  }
  
  return sorted;
}

export function renderCards(container, servers, activeServerLink, pingData, sortMode, onServerSelect) {
  if (!container) return;
  container.innerHTML = '';
  
  const uniqueServers = Array.from(new Set(servers));
  const displayServers = sortServers(uniqueServers, sortMode, pingData);
  
  displayServers.forEach(link => {
    const info = parseBasicInfo(link);
    const card = document.createElement('div');
    card.className = `server-card ${activeServerLink === link ? 'selected' : ''}`;
    
    let latency = pingData[link] === 'pinging' ? '...' : (pingData[link] === -1 ? 'Err' : (pingData[link] ? `${pingData[link]}ms` : '—'));
    
    const displayName = info.name || info.address || 'Прокси';
    const displayType = info.type ? info.type.toUpperCase() : 'VPN';

    const detailsDiv = document.createElement('div');
    detailsDiv.className = 'details';
    
    const iconDiv = document.createElement('div');
    iconDiv.className = 'server-icon';
    iconDiv.textContent = '🌐';
    
    const infoDiv = document.createElement('div');
    const titleH4 = document.createElement('h4');
    titleH4.style.fontSize = '14px';
    titleH4.style.display = 'flex';
    titleH4.style.alignItems = 'center';
    titleH4.style.gap = '8px';
    
    const protoTag = document.createElement('span');
    protoTag.className = 'protocol-tag';
    protoTag.style.background = 'var(--accent-color)';
    protoTag.style.color = 'white';
    protoTag.style.padding = '2px 6px';
    protoTag.style.borderRadius = '4px';
    protoTag.style.fontSize = '10px';
    protoTag.textContent = displayType;
    
    const nameSpan = document.createElement('span');
    nameSpan.className = 'server-name-text';
    nameSpan.textContent = displayName;
    
    titleH4.appendChild(protoTag);
    titleH4.appendChild(nameSpan);
    
    const addressP = document.createElement('p');
    addressP.style.fontSize = '11px';
    addressP.style.color = 'var(--text-dim)';
    addressP.textContent = info.address;
    
    infoDiv.appendChild(titleH4);
    infoDiv.appendChild(addressP);
    
    detailsDiv.appendChild(iconDiv);
    detailsDiv.appendChild(infoDiv);
    
    const pingDiv = document.createElement('div');
    pingDiv.className = 'ping';
    pingDiv.textContent = latency;
    
    card.appendChild(detailsDiv);
    card.appendChild(pingDiv);
    
    card.onclick = () => onServerSelect(link, displayName, displayType, info.address);
    container.appendChild(card);
  });
}

export function setSortMode(mode) {
    currentSortMode = mode;
}

export function setPingData(link, latency) {
    pingData[link] = latency;
}
