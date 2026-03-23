const express = require('express');
const app = express();
const http = require('http').Server(app);
const io = require('socket.io')(http, { cors: { origin: '*', methods: ['GET', 'POST'] } });

const PORT = process.env.PORT || 3000;
const hosts = {}; // { remoteId -> { socketId, password } }

io.on('connection', (socket) => {
    socket.on('register-host', (data) => {
        const { remoteId, password, hostname } = data;
        hosts[remoteId] = { socketId: socket.id, password, hostname: hostname || "Dispositivo VeloDesk" };
        socket.join(remoteId);
        socket.emit('host-registered', { ...hosts[remoteId], remoteId });
        console.log(`Host registrado: ${remoteId} (${hostname})`);
    });

    socket.on('request-connection', (data) => {
        const { remoteId, password } = data;
        const host = hosts[remoteId];
        if (host && host.password === password) {
            socket.join(remoteId);
        socket.emit('auth-success', { remoteId, hostname: host.hostname });
        io.to(host.socketId).emit('peer-joined', socket.id);
        console.log(`[AUTH] Sucesso: Viewer ${socket.id} -> Host ${remoteId} (${host.hostname})`);
    } else {
        console.warn(`[AUTH] Falha: Tentativa de conexão para ${remoteId} (Senha ou ID incorretos)`);
        socket.emit('auth-error', host ? 'Senha incorreta.' : 'Dispositivo offline.');
    }
});

    socket.on('signal', (data) => {
        if (data.to && data.signal) {
            // Send only to others in the room (or the specific target)
            socket.to(data.to).emit('signal', { from: socket.id, signal: data.signal });
        }
    });

    socket.on('disconnect', () => {
        for (const [id, host] of Object.entries(hosts)) {
            if (host.socketId === socket.id) {
                delete hosts[id];
                break;
            }
        }
    });
});

app.get('/', (req, res) => res.send('VeloDesk Proxy Active'));
http.listen(PORT, () => console.log(`Signaling on port ${PORT}`));
