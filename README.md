# VeloDesk - Remote Desktop WebRTC

VeloDesk é uma solução de acesso remoto de alto desempenho baseada em WebRTC, projetada para oferecer baixa latência e controle fluido. O sistema é composto por três componentes principais: um servidor de sinalização, um motor de captura (Host) e uma interface de visualização (Viewer).

## 🚀 Arquitetura do Sistema

O projeto utiliza o protocolo WebRTC para estabelecer uma conexão direta ponto-a-ponto (P2P) entre o computador controlado e o visualizador, garantindo que o fluxo de vídeo e os comandos de entrada não passem pelo servidor após a conexão inicial.

### Componentes

1.  **Signaling Server (`/signaling-server`)**
    *   **Tecnologia:** Node.js, Express, Socket.io.
    *   **Função:** Atua como um "intermediário" para que os dois lados se encontrem. Ele troca as ofertas (Offers), respostas (Answers) e candidatos de rede (ICE Candidates).
    *   **Implantado em:** Render.com (Web Service).

2.  **Capture Motor / Host (`/capture-motor`)**
    *   **Tecnologia:** Python, aiortc, MSS, PyAutoGUI, OpenCV.
    *   **Função:** Captura a tela do computador host em tempo real, codifica o vídeo e o envia via WebRTC. Também recebe comandos de teclado/mouse via um DataChannel seguro e os executa localmente.
    *   **Destaque:** Renderização manual do cursor para visibilidade contínua no stream.

3.  **Client UI / Viewer (`/client-ui`)**
    *   **Tecnologia:** Electron (HTML/JS).
    *   **Função:** Interface onde o usuário insere o ID e Senha do dispositivo que deseja controlar. Recebe o stream de vídeo e mapeia todos os eventos de input (clique, scroll, teclado) para enviar ao Host.

## 🛠️ Como Funciona (Lógica)

1.  **Registro:** O **Host** se conecta ao servidor de sinalização e registra um ID único e uma senha.
2.  **Descoberta:** O **Viewer** solicita uma conexão ao servidor informando o ID e a senha do alvo.
3.  **Handshake WebRTC:**
    *   O Viewer cria uma "Oferta" (SDP) e a envia pelo servidor.
    *   O Host recebe a oferta, cria uma "Resposta" (Answer) e a devolve.
    *   Ambos trocam endereços de rede (ICE Candidates) via STUN para atravessar roteadores/NAT.
4.  **Conexão P2P:** Uma vez estabelecida a conexão, o vídeo flui diretamente do Host para o Viewer pelo canal de mídia, e o controle flui via DataChannel (SCTP).

## 📦 Estrutura de Pastas

```text
/
├── signaling-server/    # Servidor Node.js para troca de mensagens
├── capture-motor/      # Engine Python para captura e controle
├── client-ui/           # Aplicativo Electron (Frontend)
├── render.yaml          # Configurações de infraestrutura (Render.com)
└── DEPLOY.md            # Guia de implantação detalhado
```

## ⚙️ Pré-requisitos

*   **Node.js** (v18+)
*   **Python 3.10+**
*   **Git**

## 🔧 Instalação

### 1. Servidor de Sinalização
```bash
cd signaling-server
npm install
npm start
```

### 2. Host (Captura)
```bash
cd capture-motor
# Recomendado usar venv
python -m venv venv
venv\Scripts\activate
pip install -r requirements.txt # (ou instale aiortc mss pyautogui opencv-python numpy python-socketio)
python main.py
```

### 3. Viewer (Interface)
```bash
cd client-ui
npm install
npm start
```

## 🌐 Implantação
Para hospedar o servidor publicamente e permitir conexões entre redes diferentes, consulte o arquivo [DEPLOY.md](https://github.com/andreygiordane/VeloDesk/blob/main/DEPLOY.md).

## 🛡️ Segurança
*   Conexões protegidas por ID aleatório e senha.
*   Tráfego de mídia e controle criptografado nativamente pelo protocolo WebRTC (DTLS/SRTP).
*   Arquivos de configuração local (`velodesk_config.json`) são ignorados pelo Git.

---
Desenvolvido com foco em velocidade e transparência.
