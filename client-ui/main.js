const localDeviceIdEl = document.getElementById('localDeviceId');
const clientAliasEl = document.getElementById('clientAlias');
const targetIdInput = document.getElementById('targetIdInput');
const connectBtn = document.getElementById('connectBtn');
const openSettingsBtn = document.getElementById('openSettingsBtn');
const settingsModal = document.getElementById('settingsModal');
const saveSettingsBtn = document.getElementById('saveSettingsBtn');
const sidebarItems = document.querySelectorAll('.sidebar-item');
const sections = document.querySelectorAll('.settings-section');
const settingsPassInput = document.getElementById('settingsPassInput');
const passModal = document.getElementById('passModal');
const passInput = document.getElementById('passInput');
const signalingUrlInput = document.getElementById('signalingUrlInput');
const submitPassBtn = document.getElementById('submitPassBtn');
const closePassModal = document.getElementById('closePassModal');

const recentSection = document.getElementById('recentSection');
const recentGrid = document.getElementById('recentGrid');

let targetId = "";
let recentConnections = JSON.parse(localStorage.getItem('velodesk-recent') || '[]');

// --- RECENT CONNECTIONS LOGIC ---
function renderRecent() {
    if (recentConnections.length === 0) {
        recentSection.style.display = 'none';
        return;
    }
    recentSection.style.display = 'block';
    recentGrid.innerHTML = '';

    recentConnections.forEach(entry => {
        const id = entry.id || entry; // Support legacy string arrays
        const name = entry.name || "Dispositivo Remoto";
        
        const card = document.createElement('div');
        card.className = 'recent-card';
        card.innerHTML = `
            <div class="icon"><svg viewBox="0 0 24 24" width="16" fill="currentColor"><path d="M10 16.5l6-4.5-6-4.5v9zM12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm0 18c-4.41 0-8-3.59-8-8s3.59-8 8-8 8 3.59 8 8-3.59 8-8 8z"/></svg></div>
            <div class="info">
                <div class="name">${name}</div>
                <div class="id">${id.slice(0,3)}-${id.slice(3,6)}-${id.slice(6)}</div>
            </div>
            <button class="btn-remove" title="Remover histórico">✕</button>
        `;

        card.onclick = (e) => {
            if (e.target.classList.contains('btn-remove')) {
                e.stopPropagation();
                recentConnections = recentConnections.filter(item => (item.id || item) !== id);
                localStorage.setItem('velodesk-recent', JSON.stringify(recentConnections));
                renderRecent();
                return;
            }
            targetIdInput.value = id;
            connectBtn.click();
        };

        recentGrid.appendChild(card);
    });
}

// Function exported for shared use by viewer.js (via localStorage)
function saveConnection(id, name) {
    let history = JSON.parse(localStorage.getItem('velodesk-recent') || '[]');
    history = history.filter(item => (item.id || item) !== id);
    history.unshift({ id, name: name || "Dispositivo Remoto" });
    if (history.length > 6) history = history.slice(0, 6);
    localStorage.setItem('velodesk-recent', JSON.stringify(history));
    
    // Update local state if we are in the dashboard window
    recentConnections = history;
    renderRecent();
}

renderRecent();
window.addEventListener('storage', () => {
    recentConnections = JSON.parse(localStorage.getItem('velodesk-recent') || '[]');
    renderRecent();
});

// --- PURPLE HALFTONE WAVES ANIMATION ---
const canvas = document.getElementById('bgCanvas');
const ctx = canvas.getContext('2d');
let dots = [];

function initDots() {
    canvas.width = window.innerWidth;
    canvas.height = window.innerHeight;
    dots = [];
    const spacing = 35;
    for (let x = 0; x < canvas.width; x += spacing) {
        for (let y = 0; y < canvas.height; y += spacing) {
            dots.push({ x, y, baseSize: 1.5 });
        }
    }
}

function animateDots() {
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    const time = Date.now() * 0.001;
    
    dots.forEach(dot => {
        const dist = Math.sqrt(Math.pow(dot.x - canvas.width/2, 2) + Math.pow(dot.y - canvas.height/2, 2));
        const wave = Math.sin(dist * 0.01 - time * 2) * 0.5 + 0.5;
        const size = dot.baseSize + wave * 2.5;
        
        ctx.beginPath();
        ctx.arc(dot.x, dot.y, size, 0, Math.PI * 2);
        ctx.fillStyle = `rgba(181, 61, 210, ${0.15 + wave * 0.25})`;
        ctx.fill();
    });
    requestAnimationFrame(animateDots);
}

window.addEventListener('resize', initDots);
initDots();
animateDots();

// --- ELECTRON IPC ---
if (window.electronAPI) {
    window.electronAPI.onInitHost((config) => {
        const id = config.remoteId;
        localDeviceIdEl.innerText = `${id.slice(0, 3)}-${id.slice(3, 6)}-${id.slice(6)}`;
        clientAliasEl.innerText = config.hostname || "Este Dispositivo";
        settingsPassInput.value = config.password;
        if (window.hostPolling) clearInterval(window.hostPolling);
    });

    window.hostPolling = setInterval(() => {
        window.electronAPI.getHostConfig();
    }, 1000);
}

// --- TAB SWITCHING ---
sidebarItems.forEach(item => {
    item.addEventListener('click', () => {
        sidebarItems.forEach(i => i.classList.remove('active'));
        sections.forEach(s => s.classList.remove('active'));
        item.classList.add('active');
        document.getElementById(item.dataset.tab).classList.add('active');
    });
});

// --- SETTINGS ---
openSettingsBtn.onclick = () => settingsModal.style.display = 'flex';
settingsModal.onclick = (e) => { if (e.target === settingsModal) settingsModal.style.display = 'none'; };
saveSettingsBtn.onclick = () => {
    const newPass = settingsPassInput.value;
    const newSignalingUrl = signalingUrlInput.value;
    if (window.electronAPI) window.electronAPI.updatePassword(newPass);
    localStorage.setItem('velodesk-signaling-url', newSignalingUrl);
    settingsModal.style.display = 'none';
};

// Initialize Settings
signalingUrlInput.value = localStorage.getItem('velodesk-signaling-url') || 'http://localhost:3000';

// --- NAVIGATION & CONNECTION ---
connectBtn.onclick = () => {
    targetId = targetIdInput.value.replace(/-/g, '');
    if (targetId.length < 9) return alert("ID Inválido");
    passModal.style.display = 'flex';
};

submitPassBtn.onclick = () => {
    const password = passInput.value;
    if (window.electronAPI) {
        window.electronAPI.openViewer({ remoteId: targetId, password });
        passModal.style.display = 'none';
        passInput.value = '';
    }
};

closePassModal.onclick = () => passModal.style.display = 'none';
