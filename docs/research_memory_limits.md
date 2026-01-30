#### Why Similarity Isn’t Enough for Memory



#### In today's newsletter:

```
Why similarity isn’t enough for memory.
What is Contrastive Learning?
```

# TODAY'S ISSUE

```
AGENTS
```

## Why similarity isn’t enough for memory

```
Agents are stateless by default. Every returning user is a
stranger.
The common fix is adding a vector database. But here’s
the problem: similarity isn’t memory.
```

Let’s understand this in more detail today. Later in this
article, we’ll show you an **open-source framework** to
solve this problem!

### Why similarity isn’t enough for memory

Consider this conversation:

```
Week 1: User says, “I just had a steak at Morton’s, and
it was incredible.”
Week 2: User mentions, “I decided to go vegetarian.”
Week 3: User asks, “What restaurants should I try
this weekend?”
```

The vector DB will likely return both statements as highly
relevant, and the Agent could recommend Morton’s steak.

The problem is that embeddings measure semantic
closeness, not truth.

They don’t understand that “decided to go vegetarian”
replaces “I love steak” so two food-related statements are
treated equally.

### What Agents actually need to remember

To fix this, we need to step back and understand what
memory actually means for agents.

Production Agents need two layers of memory:

```
Short-term memory tracks ongoing conversations
within a session, such as conversation history,
uploaded files, retrieved documents, and tool
outputs.
Long-term memory persists across sessions and
stores user information, preferences, learned facts,
and past experiences. This is what should survive
after the conversation ends.
```

Within long-term memory, agents need three types of sub-
memories:

```
Semantic memory stores facts and concepts, such as
“User prefers Python,” “works in fintech,” and “went
vegetarian.”
Episodic memory stores experiences and events such
as past actions, previous solutions that worked, and
the history of interactions.
```

```
Procedural memory stores instructions and
processes, including system prompts, workflow
instructions, and operational procedures.
```

### Building memory systems

Supporting short-term context, long-term persistence, and
evolving beliefs requires building a system, not a single
component.

The first piece is short-term memory (the active context
for a single session). It keeps the conversation coherent,
but once you hit the token limit, older messages are
dropped, and important context disappears.

To retain information across sessions, we add long-term
memory by extracting facts from conversations,
embedding them and storing them in a vector DB:

But memory is not static. Users change preferences and
override earlier instructions. We need to model change
over time by detecting conflicts and marking which
information is current vs. historical:

That still doesn’t solve retrieval. When a new query
comes in, the agent has to decide which version of a
memory is current. One approach is to bias retrieval
toward more recent memories, gradually reducing the
influence of older ones:

At this point, the system has short-term memory, long-
term storage, conflict resolution and temporally aware
retrieval. Building this from scratch takes significant time,
and many of the hardest problems only surface in
production.

### An open-source memory framework

Cognee is a 100% open-source framework that solves this
by combining vector search with knowledge graphs.

Here’s what makes it different:

```
Composable pipelines: Instead of locking you into a
fixed workflow, Cognee lets you customize chunking
strategies, embedding models, and entity extraction
methods within the same pipeline.
Weighted memory: Connections in the knowledge
graph are weighted based on usage. When retrieved
information contributes to a successful response, the
corresponding relationships become stronger. Over
time, the graph reflects what actually matters in
practice.
Self-improving (memify): Memory is continuously
refined through RL-inspired optimization,
strengthening useful paths, pruning stale nodes, and
auto-tuning based on real usage patterns.
```

Cognee uses three complementary storage systems: a
vector store for semantic search, a graph store for
relationships and temporal logic, and a relational store
for tracking provenance and metadata.

All the complexity we showed earlier, like managing
short-term context, evolving long-term facts, resolving
conflicts, and retrieving the right information, reduces to
six lines of code:

That’s it.

```
add() ingests your documents (text, PDFs, audio,
images)
cognify() builds the knowledge graph with entities
and relationships
memify() optimizes memory based on usage
patterns
search() retrieves with temporal awareness
```

### How Cognee handles the vegetarian scenario

Let’s replay the same scenario with Cognee handling
memory.

```
Week 1:
User says, “I just had a steak at Morton’s, and it
was incredible.”
Cognee creates: User enjoys steak, User
has_preference “meat”, positive experience at
Morton’s.
```

```
Week 2:
User says: “I decided to go vegetarian. Health
reasons, and I’m feeling great about it.”
Cognee recognizes the conflict. It archives
previous meat preference as historical, creates
a new preference “vegetarian”, marks the
change timestamp.
```

```
Week 3: “What restaurants should I try this
weekend?”
```

**Cognee’s process:**

```
Semantic search finds food preferences and
restaurant content
Graph traversal identifies that the current dietary
preference is vegetarian
```

```
Temporal logic recognizes that Week 1 preference
was replaced by Week 2 decision
Returns: “Since you recently went vegetarian, try
Gracias Madre or Crossroads Kitchen - both have
excellent plant-based options.”
```

The memory layer understands that preferences change
over time, archives outdated info as historical context,
and uses current state for recommendations.

### Conclusion

Vector search retrieves content based on similarity. But
similarity doesn’t tell you which information is current,
how facts relate to each other, or what’s been superseded
by newer information.

For production agents, you need memory that tracks
relationships between facts, understands that
information changes over time, and improves based on
actual usage.

Building this from scratch means coordinating vector
stores, graph databases, and custom retrieval logic. That’s
weeks of work and a lot of edge cases you won’t see until
production.

Cognee provides these capabilities as a single open-source
system, so you don’t have to assemble and maintain them
yourself.

**Here’s the GitHub repo →**

MACHINE LEARNING

## What is Contrastive Learning?

Contrastive Learning is a popular self-supervised learning
technique that teaches models to learn useful
representations by comparing samples.

Let’s understand more by considering a real-world task.

As an ML engineer, imagine you are responsible for
building a face unlock system.

Let’s look through some possible options:

### 1) Building a Binary classifier

Output 1 if the true user is opening the mobile; 0
otherwise.

Initially, you can ask the user to input facial data to train
the model.

But that’s where you identify the problem.

Their inputs will belong to “Class 1.”

Now, you can’t ask the user to find someone to volunteer
for “Class 0” samples since it’s too much hassle for them.

Also, you need diverse “Class 0” samples. Samples from
just one or two faces might not be sufficient.

The next possible solution you think of is...

Maybe ship some negative samples (Class 0) to the device
to train the model.

Might work.

But then you realize another problem:

What if another person wants to use the same device?

Since all new samples will belong to the “new face”
during adaptation, what if the model forgets the first
face?

### 2) How about transfer learning?

```
Train a neural network model (base model) on some
related task → This will happen before shipping the
model to the user’s device.
Next, replace the last few layers of the base model
with untrained layers and ship it to the device.
```

The first few layers would have learned to identify the
key facial features, and from there on, training on the
user’s face won’t require much data.

But yet again, you realize that you shall run into the same
problems you observed with the binary classification
model, since the new layers will still be designed to
predict 1 or 0.

### Solution: Contrastive learning using Siamese

### Networks

At its core, a Siamese network determines whether two
inputs are similar.

It does this by learning to effectively map both inputs to a
shared embedding space ( **the blue layer above** ):

```
If the distance between the embeddings is LOW, they
are similar.
If the distance between the embeddings is HIGH,
they are dissimilar.
```

They are beneficial for tasks where the goal is to compare
two data points rather than to classify them into
predefined categories/classes.

This is how it will work in our case:

Create a dataset of face pairs:

```
If a pair belongs to the same person, the true label
will be 0.
If a pair belongs to different people, the true label
will be 1.
```

After creating this data, define a network like this:

Pass both inputs through the same network to generate
two embeddings.

```
If the true label is 0 (same person) → minimize the
distance between the two embeddings.
If the true label is 1 (different person) → maximize
the distance between the two embeddings.
```

**Contrastive loss** (defined below) helps us train such a
model:

where:

```
y is the true label.
D is the distance between two embeddings.
margin is a hyperparameter, typically greater than 1.
```

Here’s how this particular loss function helps:

```
When y=1 (different people), the loss will be the
following, which will be minimum when D is close to
the margin value, leading to more distance between
the embeddings.
```

```
When y=0 (same person), the loss will be the
following, which will be minimum when D is close to
0, leading to a low distance between the embeddings.
```

This way, we can ensure that:

```
when the inputs are similar, they lie closer in the
embedding space.
when the inputs are dissimilar, they lie far in the
embedding space.
```

### Siamese Networks in face unlock

Here’s how it will help in the face unlock application.

First, train the model on several image pairs using
contrastive loss.

This model (likely after **model compression** ) will be
shipped to the user’s device.

During the setup phase, the user will provide facial data,
which will create a user embedding:

This embedding will be stored in the device’s memory.

Next, when the user wants to unlock the mobile, a new
embedding can be generated and compared against the
available embedding:

```
Action: Unlock the mobile if the distance is small.
```

Done!

Note that no further training was required here, like in
the earlier case of binary classification.

Also, what if multiple people want to add their face IDs?

No problem.

Create another embedding for the new user.

During unlock, compare the incoming user against all
stored embeddings.

Here’s some further hands-on reading to learn how to
build on-device ML applications:

```
Learn how to build privacy-first ML systems (with
implementations): Federated Learning: A Critical
Step Towards Privacy-Preserving Machine
Learning.
Learn how to compress ML models and reduce costs:
Model Compression: A Critical Step Towards
Efficient Machine Learning.
```

👉 Over to you: Siamese Networks are not the only way to
solve this problem. What other architectures can work?


