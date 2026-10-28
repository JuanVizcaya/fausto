# Keycloak & PostgreSQL Local Environment

This repository contains a local development environment using Docker Compose to provision a Keycloak Identity and Access Management server backed by a PostgreSQL database. It includes commented-out configurations for future Spring Boot (Backend) and React (Frontend) applications.

## Prerequisites
* Docker installed and running
* Docker Compose installed (included with Docker Desktop)

## 🚀 Running the Environment

To build and start the containers in the background (detached mode), from the repository root (where `compose.yaml` lives) run:

```bash
docker compose up -d
```

*Note: The Keycloak container waits for the PostgreSQL database to be fully healthy before starting. It may take a minute or two for Keycloak to completely boot up on the first run.*

To verify that the services started successfully or to troubleshoot, you can view the live logs:

```bash
docker compose logs -f keycloak
```
*(Press `Ctrl+C` to exit the log view)*

## 🔐 Accessing the Keycloak Admin Console

Once the `keycloak` container has finished starting up, you can access the administration console via your web browser:

* **URL:** [http://localhost:8080](http://localhost:8080)
* **Admin Console Direct URL:** [http://localhost:8080/admin](http://localhost:8080/admin)
* **Username:** `admin`
* **Password:** `admin_password`

From here, you can start creating realms, clients, and users for your future backend and frontend applications.

## 🛑 Stopping the Environment

When you are done developing, you can stop the running services and remove the containers/networks with the following command:

```bash
docker compose down
```

### Wiping Database Data (Complete Reset)
Because this setup uses Docker volumes (`postgres_data`), your Keycloak data (realms, users, clients) will persist even after you run `docker compose down`. 

If you want to **completely wipe all data** and start fresh, add the `-v` (volumes) flag:

```bash
docker compose down -v
```
**Warning:** This will permanently delete your PostgreSQL database volume. Any configurations made in the Keycloak console will be lost.