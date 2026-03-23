const remoteId = new URLSearchParams(window.location.search).get('id');
const password = new URLSearchParams(window.location.search).get('pass');

const videoEl = document.getElementById('remoteVideo');
const statusMsg = document.getElementById('status-msg');
const targetName = document.getElementById('target-name');
const loading = document.getElementById('loading');
const debugEl = document.getElementById('viewerDebug');
const disconnectBtn = document.getElementById('disconnectBtn');

function log(msg) {
    console.log(msg);
    if (debugEl) {
        const div = document.createElement('div');
        div.innerText = `> ${msg}`;
        debugEl.appendChild(div);
        debugEl.scrollTop = debugEl.scrollHeight;
    }
}

targetName.innerText = `ID: ${remoteId}`;

const signalingUrl = localStorage.getItem('velodesk-signaling-url') || 'https://velodesk-signaling.onrender.com';
const socket = io(signalingUrl);
let pc = null;
let dataChannel = null;

const rtcConfig = { iceServers: [{ urls: 'stun:stun.l.google.com:19302' }] };

socket.on('connect', () => {
    log("Sinalizador: Conectado.");
    socket.emit('request-connection', { remoteId, password });
});

socket.on('auth-success', async (data) => {
    log(`Dashboard: Autorizado (${data.hostname || 'Remoto'}).`);
    statusMsg.innerText = "Autorizado. Criando conexão P2P...";
    saveToHistory(remoteId, data.hostname);
    await startP2P();
});

function saveToHistory(id, name) {
    let history = JSON.parse(localStorage.getItem('velodesk-recent') || '[]');
    history = history.filter(item => (item.id || item) !== id);
    history.unshift({ id, name: name || "Dispositivo Remoto" });
    if (history.length > 6) history = history.slice(0, 6);
    localStorage.setItem('velodesk-recent', JSON.stringify(history));
}

socket.on('auth-error', (err) => {
    log(`Erro: ${err}`);
    statusMsg.innerText = `Erro: ${err}`;
});

async function startP2P() {
    log("RTC: Iniciando PeerConnection...");
    pc = new RTCPeerConnection(rtcConfig);

    pc.addTransceiver('video', { direction: 'recvonly' });
    dataChannel = pc.createDataChannel("input");

    dataChannel.onopen = () => {
        log("Canal INPUT: Aberto!");
        // Heartbeat para debug
        setInterval(() => {
            if (dataChannel.readyState === "open") {
                dataChannel.send(JSON.stringify({ action: 'heartbeat' }));
            }
        }, 1000);
    };
    dataChannel.onclose = () => log("Canal INPUT: Fechado.");
    dataChannel.onerror = (e) => log(`Canal INPUT: Erro: ${e}`);

    pc.oniceconnectionstatechange = () => {
        log(`ICE: ${pc.iceConnectionState}`);
        statusMsg.innerText = `Rede: ${pc.iceConnectionState}`;
    };

    pc.onicecandidate = (event) => {
        if (event.candidate) {
            socket.emit('signal', { to: remoteId, signal: event.candidate });
        }
    };

    pc.ontrack = (event) => {
        log("RTC: Transmissão recebida!");
        videoEl.srcObject = event.streams[0];
        loading.style.opacity = '0';
        setTimeout(() => loading.style.display = 'none', 500);
        statusMsg.innerText = "Transmissão Ativa";
        videoEl.play().catch(e => log(`Erro Play: ${e}`));
    };

    pc.ondatachannel = (event) => {
        log(`Canal remoto detectado: ${event.channel.label}`);
    };

    log("RTC: Criando Oferta...");
    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);
    socket.emit('signal', { to: remoteId, signal: { sdp: offer.sdp, type: offer.type } });
}

socket.on('signal', async (data) => {
    const payload = data.signal;
    if (payload.action === 'end-session') {
        log("Sessão encerrada pelo outro lado.");
        closeSession(false);
    } else if (payload.type === 'answer') {
        log("RTC: Resposta recebida.");
        if (pc) await pc.setRemoteDescription(new RTCSessionDescription(payload));
    } else if (payload.candidate) {
        if (pc) await pc.addIceCandidate(new RTCIceCandidate(payload));
    }
});

function closeSession(notify = true) {
    if (notify) {
        socket.emit('signal', { to: remoteId, signal: { action: 'end-session' } });
    }
    if (pc) {
        pc.close();
        pc = null;
    }
    statusMsg.innerText = "Sessão Encerrada";
    setTimeout(() => { window.close(); }, 500);
}

disconnectBtn.onclick = () => closeSession();
window.onbeforeunload = () => {
    socket.emit('signal', { to: remoteId, signal: { action: 'end-session' } });
};

// Input Mapping
const sendInput = (data) => {
    if (dataChannel && dataChannel.readyState === "open") {
        dataChannel.send(JSON.stringify(data));
    }
};

window.addEventListener('keydown', (e) => {
    e.preventDefault();
    sendInput({ action: 'keydown', key: e.key });
});

window.addEventListener('keyup', (e) => {
    sendInput({ action: 'keyup', key: e.key });
});

videoEl.onmousemove = (e) => {
    const rect = videoEl.getBoundingClientRect();
    const x = (e.clientX - rect.left) / rect.width;
    const y = (e.clientY - rect.top) / rect.height;
    sendInput({ action: 'mousemove', x, y });
};

videoEl.onmousedown = (e) => {
    const buttons = ["left", "middle", "right"];
    sendInput({ action: 'mousedown', button: buttons[e.button] || "left" });
};

videoEl.onmouseup = (e) => {
    const buttons = ["left", "middle", "right"];
    sendInput({ action: 'mouseup', button: buttons[e.button] || "left" });
};

videoEl.oncontextmenu = (e) => e.preventDefault();

videoEl.onwheel = (e) => {
    e.preventDefault();
    sendInput({ action: 'scroll', delta: e.deltaY });
};
