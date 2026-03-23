# Guia de Implantação no Render.com

Este projeto está configurado para ser implantado no [Render.com](https://render.com) usando **Blueprints**, o que automatiza a configuração do servidor de sinalização.

## 1. Preparação
Certifique-se de que seu código está em um repositório no **GitHub**, **GitLab** ou **Bitbucket**.

## Opção A: Usando Blueprint (Recomendado)
1. Faça login no [Dashboard do Render](https://dashboard.render.com).
2. Clique no botão **"New +"** e selecione **"Blueprint"**.
3. Conecte seu repositório (ex: GitHub) e clique em **"Apply"**.

## Opção B: Criação Manual (Web Service)
Se preferir não usar o Blueprint:
1. Clique em **"New +"** -> **"Web Service"**.
2. Escolha o repositório.
3. Configure os detalhes:
   - **Name**: `velodesk-signaling`
   - **Runtime**: `Node`
   - **Root Directory**: `signaling-server`
   - **Build Command**: `npm install`
   - **Start Command**: `npm start`
4. Clique em **"Advanced"** -> **"Add Environment Variable"**:
   - **Key**: `PORT` | **Value**: `10000`

## 3. Comunicação Entre Redes Diferentes
O WebRTC usa o protocolo P2P. Se um computador estiver atrás de um firewall rígido (redes corporativas ou simétricas), a conexão pode falhar.

- **STUN (Google)**: Já está configurado no projeto e resolve 80% dos casos.
- **TURN**: Necessário para os 20% restantes (retransmissão por servidor). Se precisar de um TURN estável, recomendamos serviços como [Metered.ca](https://www.metered.ca/) ou [OpenRelay](https://www.metered.ca/tools/openrelay/).

## 4. Configuração do Cliente (VeloDesk)
Após a implantação, o Render fornecerá uma URL (ex: `https://velodesk-signaling.onrender.com`).
No seu aplicativo cliente:
1. Abra as **Configurações** e cole a URL no campo correspondente.

---
**Dica:** O Render coloca serviços gratuitos em "sleep" após 15 minutos de inatividade. A primeira conexão do dia pode demorar cerca de 30 segundos para "acordar" o servidor.
