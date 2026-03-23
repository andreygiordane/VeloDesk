# Como Gerar o Setup.exe (Instalador)

Para gerar o seu instalador `setup.exe` personalizado, siga estes passos simples no seu computador:

## 1. Pré-requisitos
Certifique-se de ter o **Node.js** e o **Python** instalados na sua máquina.

## 2. Gerar o Motor de Captura (Host)
Primeiro, vamos transformar o script Python em um executável:
```bash
cd capture-motor
venv\Scripts\activate
pip install pyinstaller
pyinstaller --onefile --noconsole --name velodesk-host main.py
```
Após terminar, **mova** o arquivo gerado em `capture-motor/dist/velodesk-host.exe` para `client-ui/resources/`.

## 3. Gerar o Instalador (setup.exe)
Agora, vamos empacotar tudo no Electron:

```bash
cd client-ui
npm install
npm run build
npm run dist
```

## 3. Onde encontrar o arquivo?
Após o comando terminar, o instalador estará na pasta:
`client-ui/dist-installer/VeloDesk Setup.exe`

---

### O que este instalador faz?
- **Instalação Automática**: Cria um atalho na área de trabalho.
- **Tudo-em-Um**: O motor de captura Python (`velodesk-host.exe`) já vem embutido. Quem instalar não precisa ter Python ou Node.js.
- **Multi-uso**: O mesmo app serve para **Enviar Acesso** (Host) e **Receber Acesso** (Viewer).

### Dica de Segurança
Como o executável não possui assinatura digital (que é paga), o Windows SmartScreen pode exibir um alerta. Basta clicar em "Mais Informações" -> "Executar assim mesmo".
