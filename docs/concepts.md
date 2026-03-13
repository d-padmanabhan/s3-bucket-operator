# Kubernetes operator concepts (CRD, CR, controller, reconciler, operator)

This doc captures the core terms used in this repo and in the Kubebuilder/operator
ecosystem.

## Definitions

### CustomResourceDefinition (CRD)

The **schema**. It’s a Kubernetes API object that tells the API server: “a new
resource type exists; here’s what it looks like.”

- **No logic**
- **No behavior**
- **Pure structure**

Example (simplified):

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: s3buckets.aws.techfueled.dev
spec:
  group: aws.techfueled.dev
  names:
    kind: S3Bucket
    plural: s3buckets
  scope: Namespaced
  versions:
    - name: v1alpha1
      schema:
        openAPIV3Schema: ... # generated from +kubebuilder markers
```

In this repo, `make manifests` generates the CRD YAML from your Go type markers
and `make install` applies it to the cluster.

### Custom Resource (CR)

An **instance** of a CRD - a specific object created by a user using the new
type.

CRD is to CR what a class is to an object (OOP).

```yaml
apiVersion: aws.techfueled.dev/v1alpha1
kind: S3Bucket
metadata:
  name: my-app-assets
spec:
  bucketName: my-app-assets-prod
  region: us-east-1
```

Without a controller, this CR just sits in etcd doing nothing. Kubernetes stores
it but takes no action.

### Controller

The **watch + queue machinery** that detects changes to resources and triggers
reconciliation.

In Kubebuilder terms, this is the `SetupWithManager` registration (watches,
predicates, ownership, etc.).

### Reconciler

The **business logic** that runs when the controller says something needs
attention:

- fetches state
- compares desired vs actual
- acts to reduce drift
- updates status

In this repo, the logic lives in `S3BucketReconciler.Reconcile(...)`.

### Operator

The **complete package** (pattern), not a Kubernetes primitive:

```text
Operator = CRD(s) + Controller(s) + Reconciler(s) + Domain Knowledge
```

The key differentiator is **domain knowledge** — an operator encodes how a human
would operate a specific system.

## How they fit together

```text
┌──────────────────────────────────────────────────────────────┐
│                           OPERATOR                            │
│                                                               │
│  ┌─────────────────────────────────────────────────────────┐  │
│  │                     CRD (Schema)                        │  │
│  │     "S3Bucket exists, here's its shape"                  │  │
│  └─────────────────────────────────────────────────────────┘  │
│                          │                                     │
│                  user creates a CR                             │
│                          │                                     │
│                          ▼                                     │
│  ┌─────────────────────────────────────────────────────────┐  │
│  │                 CR (Instance in etcd)                    │  │
│  │   name: my-app-assets                                    │  │
│  │   spec.bucketName: my-app-assets-prod                    │  │
│  └─────────────────────────────────────────────────────────┘  │
│                          │                                     │
│                 controller detects change                       │
│                          │                                     │
│                          ▼                                     │
│  ┌─────────────────────────────────────────────────────────┐  │
│  │                  Controller (Watch)                      │  │
│  │   sees CR created → enqueues "default/my-app-assets"      │  │
│  └─────────────────────────────────────────────────────────┘  │
│                          │                                     │
│                          ▼                                     │
│  ┌─────────────────────────────────────────────────────────┐  │
│  │                Reconciler (Your Logic)                   │  │
│  │   fetch → bucket exists in AWS? No → create → Ready       │  │
│  └─────────────────────────────────────────────────────────┘  │
│                                                               │
└──────────────────────────────────────────────────────────────┘
```

## Parallel mental model (AWS)

If you come from AWS, a helpful framing is: **an operator is “IaC + a control
loop”**.

> [!NOTE]
> This is an analogy, not a perfect 1:1 mapping. Kubernetes is a continuous
> reconciliation system; many AWS workflows are “apply once” unless you build a
> loop around them.

### CloudFormation-style analogy

- **CRD** → a *resource type schema* (like “what properties exist for a thing”)
- **CR** → a *desired configuration instance* (like a resource block in a
  template)
- **Controller/Reconciler** → the *engine* that turns desired config into real
  resources (similar to CloudFormation creating/updating resources from a stack)
- **Status/Conditions** → *Describe* output + deployment status (e.g. CREATE\_IN\_PROGRESS,
  CREATE\_FAILED), surfaced in a Kubernetes-native way

### Service API + drift correction analogy

For AWS services, think in terms of API calls:

- **Spec** ≈ inputs you would pass to `CreateBucket`, `PutBucketVersioning`,
  `PutBucketTagging`, etc.
- **Status** ≈ what you’d learn from `HeadBucket`, `GetBucketVersioning`,
  `GetBucketTagging`, plus derived health signals
- **Reconcile** ≈ the loop that repeatedly:
  - calls `Describe`/`Get` APIs to observe
  - calls `Create`/`Update`/`Delete` APIs to converge

### Finalizers vs AWS deletion behavior

Finalizers are the Kubernetes way to guarantee cleanup of **external state**.
If you’ve used CloudFormation, the closest mental model is “stack deletion owns
resource deletion unless you explicitly retain it”. In Kubernetes, the finalizer
is how the controller “owns” deletion of the external resource when the CR is
deleted.

### Event-driven AWS analogy (Lambda + EventBridge + DynamoDB)

Another useful parallel is to model the Kubernetes API as your “source of truth”
store, and the controller as an event-driven worker.

Here’s a practical mapping:

- **Kubernetes API (etcd)** → **DynamoDB table** storing “desired config” items
- **CRD schema** → **validation/schema registry** (think EventBridge Schema
  Registry, API Gateway models, or app-level validation before writes)
- **CR** → **a DynamoDB item** representing desired state (`bucketName`,
  `region`, etc.)
- **Watch events** → **EventBridge events** (or DynamoDB Streams) notifying that
  an item changed
- **Controller workqueue** → **SQS queue** (buffer + retry + backoff)
- **Controller** → **EventBridge rule** that routes change events into the queue
- **Reconciler** → **Lambda handler** that consumes queue messages and makes AWS
  APIs match desired state
- **Status/conditions** → **fields on the item** (or a separate table) written
  by the Lambda to report progress/health
- **Leader election** → **DynamoDB lease/lock** so only one worker acts as the
  “active controller” at a time (when you run multiple replicas)

How it “feels” operationally:

1. User writes desired config (CR) → item written to DynamoDB (etcd)
2. Change event emitted → EventBridge routes to SQS
3. Lambda processes the message → calls S3 APIs to create/update/delete
4. Lambda writes back progress → status/conditions updated
5. Retries happen automatically on transient failures (queue + Lambda retries)

The key operator/controller properties still apply:

- **Idempotency**: the Lambda/reconciler must tolerate replays and partial
  failures (same message processed twice should converge to the same state)
- **Drift correction**: if the real S3 bucket changes out of band, the next
  reconcile should correct it (or report it clearly)

Finalizer parallel (delete flow):

- In Kubernetes, a finalizer ensures a delete request becomes “mark for delete”
  → controller cleans up external state → controller removes finalizer → object
  is actually deleted.
- In AWS terms, think: “don’t delete the DynamoDB item until the Lambda has
  finished cleanup”, implemented via a `deleting=true` flag/state machine step
  (often Step Functions + a lock/lease) rather than a hard delete.

## Practical reality in Kubebuilder

In Kubebuilder, the lines blur because the same type typically contains:

- reconciler logic (`Reconcile`)
- controller registration (`SetupWithManager`)

```go
type S3BucketReconciler struct {
  client.Client
  Scheme *runtime.Scheme
  // plus any AWS clients/config you inject
}

func (r *S3BucketReconciler) Reconcile(...) {}
func (r *S3BucketReconciler) SetupWithManager(mgr ctrl.Manager) error {}
```

## Further reading

- [Kubernetes series part 1: Kubernetes Operators (Medium)](https://medium.com/@leonjasonsanto/kubernetes-series-part-1-kubernetes-operators-c592a7370d38)
