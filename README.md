# Aluguel

Aplicação de gestão de imóveis e aluguéis escrita em Go, com MySQL e interface web incorporada ao executável.

Inclui cadastros de imóveis, inquilinos, locadores, contatos e usuários; histórico dos imóveis; rateio de energia residencial e comercial; recibos em duas vias; contratos editáveis e consulta de correção pelo IPCA.

## Requisitos

- Go 1.26 ou superior para compilar.
- Acesso a um MySQL com o esquema da aplicação.
- Docker com Compose apenas para executar a suíte de integração em banco descartável.

O executável contém os templates HTML, CSS, fontes dos ícones e scripts da interface. Não precisa de Go instalado no servidor nem de uma etapa separada de build do frontend.

## Configuração e execução

Crie uma configuração local a partir do exemplo:

```sh
cp config.example.json config.json
chmod 600 config.json
```

Edite `config.json` com o endereço e as credenciais do banco. Para uma instalação já configurada, continue utilizando seu arquivo atual.

Compile e verifique a conexão e as colunas esperadas:

```sh
make build
./bin/aluguel -config config.json -check
```

Inicie o servidor:

```sh
./bin/aluguel -config config.json
```

A configuração de exemplo usa a porta `8080`. O caminho de `-config` pode ser absoluto, permitindo executar o programa a partir de qualquer diretório. Para desenvolvimento:

```sh
make run
```

### Principais opções

| Opção | Uso |
|---|---|
| `address` | Endereço HTTP, por exemplo `:8080`. |
| `database.host`, `database.port`, `database.name` | Servidor, porta e nome do MySQL. |
| `database.username`, `database.password` | Credenciais de acesso ao banco. |
| `database.timezone` | Fuso de apresentação das datas, por exemplo `America/Sao_Paulo`. |
| `database.parameters` | Opções da conexão, incluindo `tls`, `charset` e `collation`. |
| `database.max_open_connections`, `database.max_idle_connections` | Limites do pool de conexões. |
| `session_timeout_seconds` | Tempo máximo de inatividade da sessão; padrão de 1.800 segundos. |
| `secure_cookies` | Use `true` quando o acesso ocorrer por HTTPS. |

O arquivo é lido na inicialização; alterações exigem reiniciar o processo. As sessões ficam na memória, portanto uma reinicialização exige novo login.

Se o MySQL estiver em outro computador, ajuste `database.host`: `localhost` aponta para a máquina que executa a aplicação.

## Banco e regras de negócio

A aplicação usa as tabelas `tb_tenants`, `tb_landlords`, `tb_contacts`, `tb_properties`, `tb_properties_logs`, `tb_users`, `tb_energy`, `tb_commercial_energy` e `tb_energy_title`.

A inicialização verifica o esquema e não cria tabelas, altera colunas ou modifica dados automaticamente. IDs são atribuídos pelo `AUTO_INCREMENT`. Os usuários existentes continuam autenticando com seus hashes BCrypt; novas senhas usam custo 10. As permissões são `ADMIN`, `OPERADOR` e `LEITOR`.

- Cadastros, contatos, exclusões e histórico são gravados em transações.
- Excluir um inquilino libera os imóveis vinculados e registra a desocupação no histórico.
- Somente a última leitura de cada tabela de energia pode ser alterada. A regra é validada na interface e no servidor, inclusive para formulários desatualizados. O histórico completo pode ser consultado em `/energies?history=1`.
- Consumo residencial zero remove rateios anteriores. Uma conta zerada gera parcelas zero; uma conta positiva sem consumo suficiente para dividi-la fica com rateio indisponível, exibido como “—”.
- O formulário `/energies/titleform` carrega os cinco títulos atuais. Alterar apenas um preserva os demais; cada salvamento mantém um novo registro dos títulos.
- A referência do IPCA considera mudanças no valor do aluguel ou na locação. Alterar apenas endereço, observação ou dia de pagamento não reinicia essa referência.
- Os recibos de condomínio usam a data do mês seguinte, incluindo a virada do ano.
- Datas de auditoria são armazenadas e lidas em UTC. Campos `DATE` conservam a data civil, e a interface apresenta os horários no fuso configurado.

A consulta ao Bacen tem tempo limite; uma falha não impede abrir o imóvel. O editor de contratos permite alterar o texto no navegador e imprimir.

## Raspberry Pi 4 com Debian

Compile as variantes de 64 e 32 bits:

```sh
make build-rpi
```

No Raspberry, identifique a arquitetura com `dpkg --print-architecture`:

| Arquitetura do Debian | Executável |
|---|---|
| `arm64` | `bin/aluguel-linux-arm64` |
| `armhf` | `bin/aluguel-linux-armv7` |

Copie o executável correspondente e seu `config.json` para o dispositivo. Os binários são estáticos e não dependem de bibliotecas C no destino.

Exemplo para 64 bits:

```sh
chmod +x aluguel-linux-arm64
chmod 600 config.json
./aluguel-linux-arm64 -config config.json -check
./aluguel-linux-arm64 -config config.json
```

Para atualizar, pare o processo atual, substitua o executável e inicie novamente. Recarregue o navegador com `Ctrl+Shift+R` para atualizar os recursos da interface.

## Organização

```text
cmd/aluguel/                 Entrada do servidor
internal/app/               Configuração, HTTP, persistência e regras de negócio
internal/app/*_test.go       Testes unitários e de integração
internal/app/testdata/       Esquema exclusivo dos testes MySQL
web/templates/              Templates HTML nativos Go
web/static/                 CSS, scripts, ícones e bibliotecas da interface
scripts/test-mysql.sh        Execução do MySQL descartável para testes
compose.test.yml            Ambiente de integração
config.example.json         Exemplo de configuração sem credenciais reais
Makefile                    Comandos de build, execução e testes
```

As páginas mantêm os componentes, cores e navegação da aplicação. Edite os arquivos em `web/templates/` e `web/static/` diretamente; recompilar incorpora as alterações ao executável. As licenças e avisos das bibliotecas da interface ficam em `web/static/vendor/`.

## Testes

```sh
make test
go vet ./...
```

O alvo `test` executa os testes Go com detector de concorrência. Inclui autenticação, permissões, CSRF, transações, datas, rateios, proteção do histórico, títulos, documentos, renderização e escape de HTML. Os testes unitários de persistência usam SQL mock e não acessam o banco da aplicação.

Para testar as gravações em um MySQL 8.4 descartável:

```sh
make integration
```

A suíte usa a porta local `13306`, o banco `aluguel_test`, credenciais próprias e armazenamento temporário. Não lê `config.json` e remove o contêiner ao terminar. O SQL em `internal/app/testdata/mysql.sql` é exclusivo dos testes e não deve ser aplicado a um banco com dados.

Também é possível usar um banco de testes vazio, cujo nome termine em `_test`:

```sh
ALUGUEL_TEST_DSN='usuario:senha@tcp(localhost:3306)/aluguel_test' \
  go test -tags=integration -count=1 ./internal/app
```

A suíte recusa bancos que já contenham tabelas e remove as tabelas que criou ao terminar.

## Arquivos locais

`config.json`, configurações privadas, logs, diretórios de ferramentas e executáveis gerados ficam fora do versionamento pelo `.gitignore`. Publique o código-fonte e `config.example.json`; mantenha as credenciais reais apenas no ambiente de execução.
# aluguel-go
